package args

import (
	"flag"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	action            = flag.String("action", "run", "Action to take. Options: run, dry-run.")
	logLevel          = flag.String("log-level", "info", "Log level (trace, debug, info, warn, error, fatal, panic).")
	min               = flag.Int("min", 1, "Minimum number of free agents to keep alive. Minimum of 1.")
	max               = flag.Int("max", 100, "Maximum number of agents allowed.")
	rate              = flag.Duration("rate", 20*time.Second, "Wait time between polling Azure Devops and the Kubernetes API.")
	scaleDownDelay    = flag.Duration("scale-down", 15*time.Minute, "Wait time after scaling up before scaling down.")
	scaleUpDelay      = flag.Duration("scale-up", 5*time.Minute, "Wait time after scaling down before scaling up.")
	resourceType      = flag.String("type", "StatefulSet", "Resource type of the agent. Only StatefulSet is supported.")
	resourceName      = flag.String("name", "", "The name of the StatefulSet.")
	resourceNamespace = flag.String("namespace", "", "The namespace of the StatefulSet.")
	azpToken          = flag.String("token", "", "The Azure Devops token.")
	azpURL            = flag.String("url", "", "The Azure Devops URL. https://dev.azure.com/AccountName")
	port              = flag.Int("port", 10101, "The port to serve health checks and metrics.")
)

// Args holds all of the program arguments
type Args struct {
	Action string

	Min  int32
	Max  int32
	Rate time.Duration

	ScaleDown  ScaleDownArgs
	ScaleUp    ScaleUpArgs
	Logging    LoggingArgs
	Kubernetes KubernetesArgs
	AZD        AzureDevopsArgs
	Health     HealthArgs
}

// ScaleDownArgs holds all of the scale-down related args
type ScaleDownArgs struct {
	Delay time.Duration
}

// ScaleUpArgs holds all of the scale-up related args
type ScaleUpArgs struct {
	Delay time.Duration
}

// LoggingArgs holds all of the logging related args
type LoggingArgs struct {
	Level log.Level
}

// KubernetesArgs holds all of the Kubernetes related args
type KubernetesArgs struct {
	Type      string
	Name      string
	Namespace string
}

// HealthArgs holds all of the healthcheck related args
type HealthArgs struct {
	Port int
}

// FriendlyName returns the name used to reference the resource in the CLI, ex: deployment/myapp
func (a KubernetesArgs) FriendlyName() string {
	return fmt.Sprintf("%s/%s", strings.ToLower(a.Type), a.Name)
}

// AzureDevopsArgs holds all of the Azure Devops related args
type AzureDevopsArgs struct {
	Token string
	URL   string
}

// ArgsFromFlags returns an Args parsed from the program flags
func ArgsFromFlags() Args {
	// error should be validated in ValidateArgs()
	logrusLevel, _ := log.ParseLevel(*logLevel)
	return Args{
		Action: *action,
		Min:    int32(*min),
		Max:    int32(*max),
		Rate:   *rate,
		ScaleDown: ScaleDownArgs{
			Delay: *scaleDownDelay,
		},
		ScaleUp: ScaleUpArgs{
			Delay: *scaleUpDelay,
		},
		Logging: LoggingArgs{
			Level: logrusLevel,
		},
		Kubernetes: KubernetesArgs{
			Type:      *resourceType,
			Name:      *resourceName,
			Namespace: *resourceNamespace,
		},
		AZD: AzureDevopsArgs{
			Token: *azpToken,
			URL:   *azpURL,
		},
		Health: HealthArgs{
			Port: *port,
		},
	}
}

// ValidateArgs validates all of the command line arguments
func ValidateArgs() error {
	// Validate arguments
	var validationErrors []string
	_, err := log.ParseLevel(*logLevel)
	if err != nil {
		validationErrors = append(validationErrors, err.Error())
	}
	if *action != "run" && *action != "dry-run" {
		validationErrors = append(validationErrors, "Action must be \"run\" or \"dry-run\".")
	}
	if *min < 1 {
		validationErrors = append(validationErrors, "Min argument cannot be less than 1.")
	}
	if *max <= *min {
		validationErrors = append(validationErrors, "Max pods argument must be greater than the minimum.")
	}
	if rate == nil {
		validationErrors = append(validationErrors, "Rate is required.")
	} else if rate.Seconds() <= 1 {
		validationErrors = append(validationErrors, fmt.Sprintf("Rate '%s' is too low.", rate.String()))
	}
	if *resourceType != "StatefulSet" {
		validationErrors = append(validationErrors, fmt.Sprintf("Unknown resource type %s.", *resourceType))
	}
	if *resourceName == "" {
		validationErrors = append(validationErrors, fmt.Sprintf("%s name is required.", *resourceType))
	}
	if *resourceNamespace == "" {
		validationErrors = append(validationErrors, "Namespace is required.")
	}
	if *azpToken == "" {
		validationErrors = append(validationErrors, "The Azure Devops token is required.")
	}
	if *azpURL == "" {
		validationErrors = append(validationErrors, "The Azure Devops URL is required.")
	}
	if *port < 0 {
		validationErrors = append(validationErrors, "The port must be greater than 0.")
	}
	if len(validationErrors) > 0 {
		return fmt.Errorf("Error(s) with arguments:\n%s", strings.Join(validationErrors, "\n"))
	}
	return nil
}
