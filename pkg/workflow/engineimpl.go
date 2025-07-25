package workflow

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"github.com/spf13/pflag"

	"github.com/snyk/go-application-framework/pkg/analytics"
	"github.com/snyk/go-application-framework/pkg/configuration"
	"github.com/snyk/go-application-framework/pkg/networking"
	"github.com/snyk/go-application-framework/pkg/runtimeinfo"
	"github.com/snyk/go-application-framework/pkg/ui"
)

// EngineImpl is the default implementation of the Engine interface.
type EngineImpl struct {
	extensionInitializer []ExtensionInit
	workflows            map[string]Entry
	config               configuration.Configuration
	analytics            analytics.Analytics
	networkAccess        networking.NetworkAccess
	initialized          bool
	logger               *zerolog.Logger
	ui                   ui.UserInterface
	runtimeInfo          runtimeinfo.RuntimeInfo

	mu                sync.Mutex
	invocationCounter int
}

var _ Engine = (*EngineImpl)(nil)

type engineRuntimeConfig struct {
	config configuration.Configuration
	input  []Data
	ic     analytics.InstrumentationCollector
}

type EngineInvokeOption func(*engineRuntimeConfig)

func WithConfig(config configuration.Configuration) EngineInvokeOption {
	return func(e *engineRuntimeConfig) {
		e.config = config
	}
}

func WithInput(input []Data) EngineInvokeOption {
	return func(e *engineRuntimeConfig) {
		e.input = input
	}
}

func WithInstrumentationCollector(ic analytics.InstrumentationCollector) EngineInvokeOption {
	return func(e *engineRuntimeConfig) {
		e.ic = ic
	}
}

func (e *EngineImpl) GetLogger() *zerolog.Logger {
	return e.logger
}

func (e *EngineImpl) SetLogger(logger *zerolog.Logger) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger = logger

	if e.networkAccess != nil {
		e.networkAccess.SetLogger(logger)
	}
}

func (e *EngineImpl) SetConfiguration(config configuration.Configuration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.config = config

	if e.networkAccess != nil {
		e.networkAccess.SetConfiguration(config)
	}
}

// NewWorkflowIdentifier creates a new workflow identifier represented in parsed URL format.
// It accepts a command param which is converted to a dot separated string and used as the host part of the URL.
func NewWorkflowIdentifier(command string) Identifier {
	dotSeparatedCommand := strings.ReplaceAll(command, " ", ".")
	id := url.URL{Scheme: "flw", Host: dotSeparatedCommand}
	return &id
}

// GetCommandFromWorkflowIdentifier returns the command string from a workflow identifier.
// It returns an empty string if the identifier is not a workflow identifier.
//
//goland:noinspection GoUnusedExportedFunction
func GetCommandFromWorkflowIdentifier(id Identifier) string {
	if id != nil && id.Scheme == "flw" {
		spaceSeparatedCommand := strings.ReplaceAll(id.Host, ".", " ")
		return spaceSeparatedCommand
	} else {
		return ""
	}
}

// NewTypeIdentifier creates a new type identifier represented in parsed URL format.
// It accepts a workflow identifier and a data type string which is used as the path part of the URL.
func NewTypeIdentifier(workflowID Identifier, dataType string) Identifier {
	id := *workflowID
	id.Scheme = "tpe"
	id.Path = dataType
	return &id
}

// NewWorkFlowEngine is an implementation of the Engine interface.
// It is called when creating a new app engine via CreateAppEngine().
func NewWorkFlowEngine(configuration configuration.Configuration) Engine {
	engine := NewDefaultWorkFlowEngine()
	engine.SetConfiguration(configuration)
	return engine
}

// NewDefaultWorkFlowEngine is an implementation of the Engine interface.
func NewDefaultWorkFlowEngine() Engine {
	engine := &EngineImpl{
		workflows:            make(map[string]Entry),
		initialized:          false,
		extensionInitializer: make([]ExtensionInit, 0),
		invocationCounter:    0,
		logger:               &zlog.Logger,
		config:               configuration.New(),
		ui:                   ui.DefaultUi(),
	}
	return engine
}

// Init initializes the engine by setting up the necessary defaults.
func (e *EngineImpl) Init() error {
	var err error

	e.mu.Lock()
	e.invocationCounter = 0
	e.mu.Unlock()

	_ = e.GetNetworkAccess()

	for i := range e.extensionInitializer {
		err = e.extensionInitializer[i](e)
		if err != nil {
			return err
		}
	}

	// later scan here for extension binaries

	if e.analytics == nil {
		e.analytics = e.initAnalytics()
	}

	if err == nil {
		e.initialized = true
	}

	return err
}

func (e *EngineImpl) initAnalytics() analytics.Analytics {
	a := analytics.New()
	a.SetIntegration(e.config.GetString(configuration.INTEGRATION_NAME), e.config.GetString(configuration.INTEGRATION_VERSION))
	a.SetApiUrl(e.config.GetString(configuration.API_URL))
	a.SetOrg(e.config.GetString(configuration.ORGANIZATION))
	a.SetClient(func() *http.Client {
		return e.networkAccess.GetHttpClient()
	})

	return a
}

// Register registers a new workflow entry with the engine.
// In order to register a workflow, the following parameters are required:
// - id: the workflow identifier
// - config: the configuration options for the workflow
// - entryPoint: the entry point function for the workflow
func (e *EngineImpl) Register(id Identifier, config ConfigurationOptions, entryPoint Callback) (Entry, error) {
	if entryPoint == nil {
		return nil, fmt.Errorf("EntryPoint must not be nil")
	}

	if config == nil {
		return nil, fmt.Errorf("config must not be nil")
	}

	if id == nil {
		return nil, fmt.Errorf("ID must not be nil")
	}

	entry := &EntryImpl{
		visible:        true,
		expectedConfig: config,
		entryPoint:     entryPoint,
	}

	tmp := id.String()
	e.workflows[tmp] = entry

	flagset := FlagsetFromConfigurationOptions(config)
	if flagset != nil {
		err := e.config.AddFlagSet(flagset)
		if err != nil {
			return nil, err
		}
	}

	return entry, nil
}

// GetWorkflows returns a list of all registered workflows.
func (e *EngineImpl) GetWorkflows() []Identifier {
	var result []Identifier

	for k := range e.workflows {
		u, err := url.Parse(k)
		if err != nil {
			// a panic here is reasonable; how did we register an invalid URL in the first place?
			panic(fmt.Sprintf("invalid workflow url: %q", k))
		}
		result = append(result, u)
	}

	return result
}

// GetWorkflow returns the workflow entry for the given workflow identifier.
func (e *EngineImpl) GetWorkflow(id Identifier) (Entry, bool) {
	workflow, ok := e.workflows[id.String()]
	return workflow, ok
}

// Deprecated: Use Invoke() with WithInput() instead
//
// InvokeWithInput invokes the workflow with the given identifier and input data.
func (e *EngineImpl) InvokeWithInput(id Identifier, input []Data) ([]Data, error) {
	return e.Invoke(id, WithInput(input))
}

// Deprecated: Use Invoke() with WithConfig() instead
//
// InvokeWithConfig invokes the workflow with the given identifier and configuration.
func (e *EngineImpl) InvokeWithConfig(id Identifier, config configuration.Configuration) ([]Data, error) {
	return e.Invoke(id, WithConfig(config))
}

// Deprecated: Use Invoke() with WithInput() and WithConfig() instead
//
// InvokeWithInputAndConfig invokes the workflow with the given identifier, input data, and configuration.
func (e *EngineImpl) InvokeWithInputAndConfig(
	id Identifier,
	input []Data,
	config configuration.Configuration,
) ([]Data, error) {
	return e.Invoke(id, WithConfig(config), WithInput(input))
}

// Invoke invokes the workflow with the given identifier.
func (e *EngineImpl) Invoke(
	id Identifier,
	opts ...EngineInvokeOption,
) ([]Data, error) {
	var output []Data
	var err error

	if !e.initialized {
		return output, fmt.Errorf("workflow must be initialized with init() before it can be invoked")
	}

	workflow, ok := e.GetWorkflow(id)
	if ok {
		callback := workflow.GetEntryPoint()
		if callback != nil {
			e.mu.Lock()
			e.invocationCounter++

			// create default options
			options := engineRuntimeConfig{
				config: e.config.Clone(),
				input:  []Data{},
			}

			// override default options based on optional parameters
			for _, opt := range opts {
				opt(&options)
			}

			// prepare logger
			prefix := fmt.Sprintf("%s:%d", id.Host, e.invocationCounter)
			localLogger := e.logger.With().Str("ext", prefix).Logger()

			localUi := e.ui

			var localAnalytics analytics.Analytics
			if options.ic != nil {
				tmpAnalytics := &analytics.AnalyticsImpl{}
				tmpAnalytics.SetInstrumentation(options.ic)
				localAnalytics = NewAnalyticsWrapper(tmpAnalytics, id.Host)
			} else {
				localAnalytics = NewAnalyticsWrapper(e.analytics, id.Host)
			}

			// prepare networkAccess
			localNetworkAccess := e.networkAccess.Clone()
			localNetworkAccess.SetConfiguration(options.config)

			localEngine := &engineWrapper{
				WrappedEngine:                   e,
				defaultInstrumentationCollector: options.ic,
			}
			e.mu.Unlock()

			// create a context object for the invocation
			context := NewInvocationContext(id, options.config, localEngine, localNetworkAccess, localLogger, localAnalytics, localUi)

			// invoke workflow through its callback
			localLogger.Printf("Workflow Start")
			output, err = callback(context, options.input)
			localLogger.Printf("Workflow End")
		}
	} else {
		err = fmt.Errorf("workflow '%v' not found", id)
	}

	return output, err
}

// GetAnalytics returns the analytics object.
func (e *EngineImpl) GetAnalytics() analytics.Analytics {
	return e.analytics
}

// GetNetworkAccess returns the network access object.
func (e *EngineImpl) GetNetworkAccess() networking.NetworkAccess {
	if e.networkAccess == nil {
		e.networkAccess = networking.NewNetworkAccess(e.config)
		e.networkAccess.SetLogger(e.logger)
	}

	return e.networkAccess
}

// AddExtensionInitializer adds an extension initializer to the engine.
func (e *EngineImpl) AddExtensionInitializer(initializer ExtensionInit) {
	e.extensionInitializer = append(e.extensionInitializer, initializer)
}

// GetConfiguration returns the configuration object.
func (e *EngineImpl) GetConfiguration() configuration.Configuration {
	return e.config
}

func (e *EngineImpl) GetUserInterface() ui.UserInterface {
	return e.ui
}

func (e *EngineImpl) SetUserInterface(userInterface ui.UserInterface) {
	e.ui = userInterface
}

func (e *EngineImpl) GetRuntimeInfo() runtimeinfo.RuntimeInfo {
	return e.runtimeInfo
}

func (e *EngineImpl) SetRuntimeInfo(ri runtimeinfo.RuntimeInfo) {
	e.runtimeInfo = ri
}

// GetGlobalConfiguration returns the global configuration options.
//
//goland:noinspection GoUnusedExportedFunction
func GetGlobalConfiguration() ConfigurationOptions {
	globalFLags := pflag.NewFlagSet("global", pflag.ContinueOnError)
	globalFLags.String(configuration.ORGANIZATION, "", "")
	globalFLags.BoolP(configuration.DEBUG, "d", false, "")
	globalFLags.Bool(configuration.INSECURE_HTTPS, false, "")
	return ConfigurationOptionsFromFlagset(globalFLags)
}
