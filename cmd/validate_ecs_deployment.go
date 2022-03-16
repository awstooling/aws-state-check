package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"io/ioutil"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	elb2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

type (
	serviceSpec struct {
		ECSClusterARN          string `json:"ecs_cluster_arn"`
		ECSClusterARNSSMParam  string `json:"ecs_cluster_arn_ssm_param"`
		ECSServiceFamily       string `json:"ecs_service_family"`
		TargetGroupARN         string `json:"target_group_arn"`
		TargetGroupARNSSMParam string `json:"target_group_arn_ssm_param"`
		Image                  string `json:"image"`
		ECSHealthCheck         bool   `json:"ecs_health_check"`
		TaskCount              int    `json:"task_count"`
		TimeoutSeconds         int    `json:"timeout_seconds"`
	}

	flagEnvKey struct {
		flag, envKey string
	}
)

const (
	DefaultTimeoutSeconds      = 300
	DefaultPollIntervalSeconds = 15
)

var (
	taskCountFlagEnvKey              = flagEnvKey{envKey: "TASK_COUNT", flag: "taskCount"}
	serviceSpecFlagEnvKey            = flagEnvKey{envKey: "SERVICE_SPEC", flag: "serviceSpec"}
	ecsClusterArnFlagEnvKey          = flagEnvKey{envKey: "ECS_CLUSTER_ARN", flag: "ecsClusterArn"}
	targetGroupArnFlagEnvKey         = flagEnvKey{envKey: "TARGET_GROUP_ARN", flag: "targetGroupArn"}
	ecsClusterArnSsmParamFlagEnvKey  = flagEnvKey{envKey: "ECS_CLUSTER_ARN_SSM_PARAM", flag: "ecsClusterArnSsmParam"}
	targetGroupArnSsmParamFlagEnvKey = flagEnvKey{envKey: "TARGET_GROUP_ARN_SSM_PARAM", flag: "targetGroupArnSsmParam"}
	ecsServiceFamilyFlagEnvKey       = flagEnvKey{envKey: "ECS_SERVICE_FAMILY", flag: "ecsServiceFamily"}
	ecsHealthCheckFlagEnvKey         = flagEnvKey{envKey: "ECS_HEALTH_CHECK", flag: "ecsHealthCheck"}
	timeoutSecondsFlagEnvKey         = flagEnvKey{envKey: "TIMEOUT_SECONDS", flag: "timeoutSeconds"}
	imageFlagEnvKey                  = flagEnvKey{envKey: "IMAGE", flag: "image"}

	serviceSpecFile string

	ecsClusterArn          string
	ecsClusterArnSsmParam  string
	ecsServiceFamily       string
	targetGroupArn         string
	targetGroupArnSsmParam string
	image                  string
	ecsHealthCheck         bool
	taskCount              int
	timeoutSeconds         int

	validateEcsDeploymentCmd = &cobra.Command{
		Use:   "validate-ecs-deployment",
		Short: "Validate successful deployment of service to ECS",
		Run:   validateEcsDeployment}
)

func validateEcsDeployment(cmd *cobra.Command, args []string) {

	spec := serviceSpec{}

	if serviceSpecFile != "" {
		file, err := ioutil.ReadFile(serviceSpecFile)
		handlerErrQuit(err)

		err = json.Unmarshal(file, &spec)

		handlerErrQuit(err)
	} else {
		spec.ECSClusterARN = viper.GetString(ecsClusterArnFlagEnvKey.envKey)
		spec.ECSClusterARNSSMParam = viper.GetString(ecsClusterArnSsmParamFlagEnvKey.envKey)
		spec.TaskCount = viper.GetInt(taskCountFlagEnvKey.envKey)
		spec.Image = viper.GetString(imageFlagEnvKey.envKey)
		spec.ECSHealthCheck = viper.GetBool(ecsHealthCheckFlagEnvKey.envKey)
		spec.TargetGroupARN = viper.GetString(targetGroupArnFlagEnvKey.envKey)
		spec.TargetGroupARNSSMParam = viper.GetString(targetGroupArnSsmParamFlagEnvKey.envKey)
		spec.ECSServiceFamily = viper.GetString(ecsServiceFamilyFlagEnvKey.envKey)
		spec.TimeoutSeconds = viper.GetInt(timeoutSecondsFlagEnvKey.envKey)
	}

	if spec.TimeoutSeconds == 0 {
		spec.TimeoutSeconds = DefaultTimeoutSeconds
	}

	cfg, err := config.LoadDefaultConfig(context.TODO())
	handlerErrQuit(err)

	ssmClient := ssm.NewFromConfig(cfg)
	spec.ECSClusterARN = getArn(spec.ECSClusterARN, spec.ECSClusterARNSSMParam, ssmClient)
	spec.TargetGroupARN = getArn(spec.TargetGroupARN, spec.TargetGroupARNSSMParam, ssmClient)

	validateSpec(spec)

	ecsClient := ecs.NewFromConfig(cfg)
	lbClient := elasticloadbalancingv2.NewFromConfig(cfg)

	conf, _ := json.MarshalIndent(spec, " ", " ")
	fmt.Println("Running ECS deployment validation for the following specification:")
	fmt.Println(string(conf))

	timeout := time.Duration(spec.TimeoutSeconds) * time.Second
	start := time.Now()
	for true {
		if doValidate(ecsClient, lbClient, &spec) {
			fmt.Println("ECS deployment validation OK")
			break
		}

		time.Sleep(time.Second * DefaultPollIntervalSeconds)

		if time.Now().Sub(start) > timeout {
			fmt.Println("Timed out trying to validate deployment")
			os.Exit(1)
			break
		}
	}
}

func validateSpec(spec serviceSpec) {
	conf, _ := json.MarshalIndent(spec, " ", " ")
	fmt.Println("Validating service spec:")
	fmt.Println(string(conf))

	if spec.ECSClusterARN == "" {
		handlerErrQuit(errors.New("ECSClusterARN must be provided"))
	}

	if spec.Image == "" {
		handlerErrQuit(errors.New("Image must be provided"))
	}

	if spec.ECSServiceFamily == "" {
		handlerErrQuit(errors.New("ECSServiceFamily must be provided"))
	}
}

func getArn(arn, ssmParam string, ssmClient *ssm.Client) string {
	if arn == "" {
		if ssmParam != "" {
			param, err := ssmClient.GetParameter(context.TODO(), &ssm.GetParameterInput{Name: &ssmParam})
			handlerErrQuit(err)
			return *param.Parameter.Value
		}
	}

	return arn
}

func doValidate(ecsClient *ecs.Client, lbClient *elasticloadbalancingv2.Client, spec *serviceSpec) bool {
	tasks, err := ecsClient.ListTasks(context.TODO(), &ecs.ListTasksInput{Cluster: &spec.ECSClusterARN, Family: &spec.ECSServiceFamily})
	if err != nil {
		fmt.Println(err.Error())
		return false
	}
	if len(tasks.TaskArns) != spec.TaskCount {
		fmt.Printf("Incorrect number of tasks found in family %v\r\n", spec.ECSServiceFamily)
		fmt.Printf("Found: %v, Expected: %v\r\n", len(tasks.TaskArns), spec.TaskCount)
		return false
	}

	taskDescs, err := ecsClient.DescribeTasks(context.TODO(), &ecs.DescribeTasksInput{Tasks: tasks.TaskArns, Cluster: &spec.ECSClusterARN})
	if err != nil {
		fmt.Println(err.Error())
		return false
	}

	containers := make([]types.Container, spec.TaskCount)
	containerIndex := 0
	for taskIndex, td := range taskDescs.Tasks {
		for _, c := range td.Containers {
			if *c.Image == spec.Image {
				containers[containerIndex] = c
				containerIndex++
				break
			}
		}

		if taskIndex != containerIndex-1 {
			fmt.Println("Task not running required image")
			return false
		}
	}

	fmt.Printf("All tasks running required image\r\n")

	allHealthChecksOk := true
	if spec.ECSHealthCheck {
		for _, c := range containers {
			fmt.Printf("Health check status for TaskARN: %v = %v\r\n", *c.TaskArn, c.HealthStatus)
			if c.HealthStatus != types.HealthStatusHealthy {
				allHealthChecksOk = false
			}
		}

		if !allHealthChecksOk {
			fmt.Println("Not all ECS health checks OK")
			return false
		}

		fmt.Println("All ECS health checks OK")
	}

	if spec.TargetGroupARN != "" {
		targetHealthOut, err := lbClient.DescribeTargetHealth(context.TODO(), &elasticloadbalancingv2.DescribeTargetHealthInput{TargetGroupArn: &spec.TargetGroupARN})
		if err != nil {
			fmt.Println(err.Error())
			return false
		}

		healthyContainersCount := 0
		for _, th := range targetHealthOut.TargetHealthDescriptions {
			fmt.Printf("Target group ARN: %v health check status = %v\r\n", spec.TargetGroupARN, th.TargetHealth.State)
			if th.TargetHealth.State == elb2types.TargetHealthStateEnumHealthy {
				for _, c := range containers {
					if len(c.NetworkInterfaces) > 0 && *c.NetworkInterfaces[0].PrivateIpv4Address == *th.Target.Id {
						healthyContainersCount++
					}
				}
			}
		}

		if healthyContainersCount < spec.TaskCount {
			fmt.Println("Not all LB health checks OK")
			return false
		}

		fmt.Println("All LB health checks OK")
	}

	return true
}

func handlerErrQuit(err error) {
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func init() {
	viper.AutomaticEnv()

	rootCmd.AddCommand(validateEcsDeploymentCmd)

	validateEcsDeploymentCmd.Flags().StringVarP(&serviceSpecFile, serviceSpecFlagEnvKey.flag, "S", "", "File containing service validation specification")
	validateEcsDeploymentCmd.Flags().StringVarP(&ecsClusterArn, ecsClusterArnFlagEnvKey.flag, "C", "", "ECS cluster ARN")
	validateEcsDeploymentCmd.Flags().StringVar(&ecsClusterArnSsmParam, ecsClusterArnSsmParamFlagEnvKey.flag, "", "ECS cluster ARN SSM Param Name")
	validateEcsDeploymentCmd.Flags().StringVarP(&ecsServiceFamily, ecsServiceFamilyFlagEnvKey.flag, "F", "", "ECS service family")
	validateEcsDeploymentCmd.Flags().StringVarP(&targetGroupArn, targetGroupArnFlagEnvKey.flag, "G", "", "Target group ARN for LB health check consideration")
	validateEcsDeploymentCmd.Flags().StringVar(&targetGroupArnSsmParam, targetGroupArnSsmParamFlagEnvKey.flag, "", "Target group ARN for LB health check consideration SSM Param name")
	validateEcsDeploymentCmd.Flags().StringVarP(&image, imageFlagEnvKey.flag, "I", "", "Task container image")
	validateEcsDeploymentCmd.Flags().BoolVarP(&ecsHealthCheck, ecsHealthCheckFlagEnvKey.flag, "T", false, "Consider ECS health check status")
	validateEcsDeploymentCmd.Flags().IntVarP(&taskCount, taskCountFlagEnvKey.flag, "", 0, "Expected task count")
	validateEcsDeploymentCmd.Flags().IntVarP(&timeoutSeconds, timeoutSecondsFlagEnvKey.flag, "O", 300, "Expected task count")

	viper.BindPFlag(taskCountFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(taskCountFlagEnvKey.flag))
	viper.BindPFlag(serviceSpecFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(serviceSpecFlagEnvKey.flag))
	viper.BindPFlag(ecsClusterArnFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(ecsClusterArnFlagEnvKey.flag))
	viper.BindPFlag(ecsClusterArnSsmParamFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(ecsClusterArnSsmParamFlagEnvKey.flag))
	viper.BindPFlag(ecsServiceFamilyFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(ecsServiceFamilyFlagEnvKey.flag))
	viper.BindPFlag(targetGroupArnFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(targetGroupArnFlagEnvKey.flag))
	viper.BindPFlag(targetGroupArnSsmParamFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(targetGroupArnSsmParamFlagEnvKey.flag))
	viper.BindPFlag(imageFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(imageFlagEnvKey.flag))
	viper.BindPFlag(ecsHealthCheckFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(ecsHealthCheckFlagEnvKey.flag))
	viper.BindPFlag(taskCountFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(taskCountFlagEnvKey.flag))
	viper.BindPFlag(timeoutSecondsFlagEnvKey.envKey, validateEcsDeploymentCmd.Flags().Lookup(timeoutSecondsFlagEnvKey.flag))
}
