package logging

import (
	"os"
	"runtime"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// StartupLogger collects Lambda identity, configuration, resources, and
// feature flags, then emits a single structured zerolog event summarising
// the cold-start state. This makes it easy to understand exactly how a
// Lambda was configured when troubleshooting from CloudWatch logs.
type StartupLogger struct {
	name         string
	commitHash   string
	buildTime    string
	initDuration time.Duration

	s3Buckets     map[string]string
	dynamoTables  map[string]string
	ssmParams     map[string]string
	stateMachines map[string]string
	lambdaFuncs   map[string]string
	features      map[string]bool
	config        map[string]string
}

// NewStartupLogger creates a StartupLogger for the given Lambda name
// (e.g. "media-lambda", "worker-lambda").
func NewStartupLogger(name string) *StartupLogger {
	return &StartupLogger{
		name:          name,
		s3Buckets:     make(map[string]string),
		dynamoTables:  make(map[string]string),
		ssmParams:     make(map[string]string),
		stateMachines: make(map[string]string),
		lambdaFuncs:   make(map[string]string),
		features:      make(map[string]bool),
		config:        make(map[string]string),
	}
}

// CommitHash sets the git commit hash baked into the binary at build time (DDR-062).
func (s *StartupLogger) CommitHash(hash string) *StartupLogger {
	s.commitHash = hash
	return s
}

// BuildTime sets the UTC build timestamp baked into the binary at build time (DDR-062).
func (s *StartupLogger) BuildTime(t string) *StartupLogger {
	s.buildTime = t
	return s
}

// S3Bucket registers an S3 bucket used by this Lambda.
func (s *StartupLogger) S3Bucket(label, name string) *StartupLogger {
	s.s3Buckets[label] = name
	return s
}

// DynamoTable registers a DynamoDB table used by this Lambda.
func (s *StartupLogger) DynamoTable(label, name string) *StartupLogger {
	s.dynamoTables[label] = name
	return s
}

// SSMParam registers an SSM parameter path loaded by this Lambda.
// Only the path is logged, never the value.
func (s *StartupLogger) SSMParam(label, path string) *StartupLogger {
	s.ssmParams[label] = path
	return s
}

// StateMachine registers a Step Functions state machine used by this Lambda.
func (s *StartupLogger) StateMachine(label, arn string) *StartupLogger {
	s.stateMachines[label] = arn
	return s
}

// LambdaFunc registers another Lambda function invoked by this Lambda.
func (s *StartupLogger) LambdaFunc(label, arn string) *StartupLogger {
	s.lambdaFuncs[label] = arn
	return s
}

// Feature registers a boolean feature flag (e.g. "instagram", "originVerify").
func (s *StartupLogger) Feature(name string, enabled bool) *StartupLogger {
	s.features[name] = enabled
	return s
}

// Config registers a non-sensitive configuration key-value pair.
func (s *StartupLogger) Config(key, value string) *StartupLogger {
	s.config[key] = value
	return s
}

// InitDuration records how long the init() function took to complete.
func (s *StartupLogger) InitDuration(d time.Duration) *StartupLogger {
	s.initDuration = d
	return s
}

// EnvOrDefault returns the value of the named environment variable, or
// defaultVal if the variable is empty or unset. Useful for resolving
// SSM parameter paths that may be overridden via environment variables.
func EnvOrDefault(envVar, defaultVal string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return defaultVal
}

// Log emits a single structured INFO log event with all collected information.
func (s *StartupLogger) Log() {
	evt := log.Info()

	// Lambda identity — auto-collected from runtime environment (DDR-062: version identity).
	lambdaDict := zerolog.Dict().
		Str("name", s.name).
		Str("functionName", os.Getenv("AWS_LAMBDA_FUNCTION_NAME")).
		Str("version", os.Getenv("AWS_LAMBDA_FUNCTION_VERSION")).
		Str("region", os.Getenv("AWS_REGION")).
		Str("memoryMB", os.Getenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE")).
		Str("logGroup", os.Getenv("AWS_LAMBDA_LOG_GROUP_NAME")).
		Str("runtime", os.Getenv("AWS_EXECUTION_ENV")).
		Str("goVersion", runtime.Version()).
		Str("arch", runtime.GOARCH).
		Str("logLevel", os.Getenv("GEMINI_LOG_LEVEL"))

	if s.commitHash != "" {
		lambdaDict = lambdaDict.Str("commitHash", s.commitHash)
	}
	if s.buildTime != "" {
		lambdaDict = lambdaDict.Str("buildTime", s.buildTime)
	}

	evt = evt.Dict("lambda", lambdaDict)

	// Resources — only non-empty maps are attached.
	resources := zerolog.Dict()
	hasResources := false

	if len(s.s3Buckets) > 0 {
		resources = resources.Dict("s3Buckets", dictFromMap(s.s3Buckets))
		hasResources = true
	}
	if len(s.dynamoTables) > 0 {
		resources = resources.Dict("dynamoTables", dictFromMap(s.dynamoTables))
		hasResources = true
	}
	if len(s.ssmParams) > 0 {
		resources = resources.Dict("ssmParams", dictFromMap(s.ssmParams))
		hasResources = true
	}
	if len(s.stateMachines) > 0 {
		resources = resources.Dict("stateMachines", dictFromMap(s.stateMachines))
		hasResources = true
	}
	if len(s.lambdaFuncs) > 0 {
		resources = resources.Dict("lambdaFunctions", dictFromMap(s.lambdaFuncs))
		hasResources = true
	}

	if hasResources {
		evt = evt.Dict("resources", resources)
	}

	// Features.
	if len(s.features) > 0 {
		d := zerolog.Dict()
		for k, v := range s.features {
			d = d.Bool(k, v)
		}
		evt = evt.Dict("features", d)
	}

	// Config.
	if len(s.config) > 0 {
		evt = evt.Dict("config", dictFromMap(s.config))
	}

	// Init duration.
	if s.initDuration > 0 {
		evt = evt.Dur("initDuration", s.initDuration)
	}

	evt.Msg("Lambda cold start complete")
}

// dictFromMap converts a map[string]string into a zerolog.Event (Dict).
func dictFromMap(m map[string]string) *zerolog.Event {
	d := zerolog.Dict()
	for k, v := range m {
		d = d.Str(k, v)
	}
	return d
}
