package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	"io/ioutil"
	"log"
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
)

var (
	serviceSpecFile string

	ecsClusterArn          string
	ecsClusterArnSsmParam  string
	ecsServiceFamily       string
	targetGroupArn         string
	targetGroupArnSsmParam string
	image                  string
	ecsHealthCheck         bool
	taskCount              int
	timeOutSeconds         int

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
		spec.ECSClusterARN = ecsClusterArn
		spec.ECSClusterARNSSMParam = ecsClusterArnSsmParam
		spec.TaskCount = taskCount
		spec.Image = image
		spec.ECSHealthCheck = ecsHealthCheck
		spec.TargetGroupARN = targetGroupArn
		spec.TargetGroupARNSSMParam = targetGroupArnSsmParam
		spec.ECSServiceFamily = ecsServiceFamily
		spec.TimeoutSeconds = timeOutSeconds
	}

	if spec.TaskCount < 1 {
		handlerErrQuit(errors.New("TaskCount must be > 0"))
	}

	if spec.TimeoutSeconds == 0 {
		spec.TimeoutSeconds = 300
	}

	cfg, err := config.LoadDefaultConfig(context.TODO())
	handlerErrQuit(err)

	ssmClient := ssm.NewFromConfig(cfg)
	spec.ECSClusterARN = getArn(spec.ECSClusterARN, spec.ECSClusterARNSSMParam, "ecsCluster", ssmClient)
	spec.TargetGroupARN = getArn(spec.TargetGroupARN, spec.TargetGroupARNSSMParam, "targetGroup", ssmClient)

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

		time.Sleep(time.Second * 10)

		if time.Now().Sub(start) > timeout {
			fmt.Println("Timed out trying to validate deployment")
			break
		}
	}

}

func getArn(arn, ssmParam, name string, ssmClient *ssm.Client) string {
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
	if len(tasks.TaskArns) == 0 {
		fmt.Printf("No tasks found in family %v\r\n", spec.ECSServiceFamily)
		return false
	}

	fmt.Printf("Number of tasks found in family: %v\r\n", len(tasks.TaskArns))

	taskDescs, err := ecsClient.DescribeTasks(context.TODO(), &ecs.DescribeTasksInput{Tasks: tasks.TaskArns, Cluster: &spec.ECSClusterARN})
	if err != nil {
		fmt.Println(err.Error())
		return false
	}

	containers := make([]types.Container, spec.TaskCount)
	containerIndex := 0
	for _, td := range taskDescs.Tasks {
		for _, c := range td.Containers {
			if *c.Image == spec.Image {
				containers[containerIndex] = c
				containerIndex++
			}

			if containerIndex == spec.TaskCount {
				break
			}
		}

		if containerIndex == spec.TaskCount {
			break
		}
	}

	if containerIndex < spec.TaskCount {
		fmt.Println("Required number of instances not found")
		return false
	}

	fmt.Printf("Required number of instances found: %v\r\n", len(containers))

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
					if *c.NetworkInterfaces[0].PrivateIpv4Address == *th.Target.Id {
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
		log.Fatal(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(validateEcsDeploymentCmd)
	validateEcsDeploymentCmd.Flags().StringVarP(&serviceSpecFile, "serviceSpec", "S", "", "File containing service validation specification")
	validateEcsDeploymentCmd.Flags().StringVarP(&ecsClusterArn, "ecsClusterArn", "C", "", "ECS cluster ARN")
	validateEcsDeploymentCmd.Flags().StringVar(&ecsClusterArnSsmParam, "ecsClusterArnSsmParam", "", "ECS cluster ARN SSM Param Name")
	validateEcsDeploymentCmd.Flags().StringVarP(&ecsServiceFamily, "ecsServiceFamily", "F", "", "ECS service family")
	validateEcsDeploymentCmd.Flags().StringVarP(&targetGroupArn, "targetGroupArn", "G", "", "Target group ARN for LB health check consideration")
	validateEcsDeploymentCmd.Flags().StringVar(&targetGroupArnSsmParam, "targetGroupArnSsmParam", "", "Target group ARN for LB health check consideration SSM Param name")
	validateEcsDeploymentCmd.Flags().StringVarP(&image, "image", "I", "", "Task container image")
	validateEcsDeploymentCmd.Flags().BoolVarP(&ecsHealthCheck, "ecsHealthCheck", "T", false, "Consider ECS health check status")
	validateEcsDeploymentCmd.Flags().IntVarP(&taskCount, "taskCount", "H", 0, "Expected task count")
	validateEcsDeploymentCmd.Flags().IntVarP(&timeOutSeconds, "timeoutOutSeconds", "O", 300, "Expected task count")
}
