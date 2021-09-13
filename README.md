# AWS State Check

## What?
This is a CLI tool that uses the AWS API to check the state of AWS. Overtime the intention is to add more commands, currently the only command is [validate-ecs-deployment](src/cmd/validate_ecs_deployment.go)

### Validate ECS Deployment
In a nutshell this command will poll AWS API until it can verify the deployment was successful or times out.  A successful verification results in exit code zero, all other results exit code 1. 

This command is coded to work for the first two uses I have, if you are lucky your use case will be a match, otherwise PR's welcome.  

Where this is brittle:

- Only considers first NIC for container
- Matches LB Target group on IP address only

There probably other issues preventing this from having a wider application/much generic use.

The best source of documentation for the command right now is the code itself: [validate-ecs-deployment](src/cmd/validate_ecs_deployment.go)

Run `awsstatecheck help validate-ecs-deployment` for flags help:

```
Validate successful deployment of service to ECS

Usage:
  use validate-ecs-deployment [flags]

Flags:
  -C, --ecsClusterArn string            ECS cluster ARN
      --ecsClusterArnSsmParam string    ECS cluster ARN SSM Param Name
  -T, --ecsHealthCheck                  Consider ECS health check status
  -F, --ecsServiceFamily string         ECS service family
  -h, --help                            help for validate-ecs-deployment
  -I, --image string                    Task container image
  -S, --serviceSpec string              File containing service validation specification
  -G, --targetGroupArn string           Target group ARN for LB health check consideration
      --targetGroupArnSsmParam string   Target group ARN for LB health check consideration SSM Param name
  -H, --taskCount int                   Expected task count
  -O, --timeoutOutSeconds int           Expected task count (default 300)

Global Flags:
      --viper   use Viper for configuration (default true)
```

## Where
Grab binaries for common OS/ARCH combos on the GitHub release page.

Get docker image here  

### Dirty Laundry
There are no tests! I will add tests on the next iteration, probably.


