package app

import (
	"log"
	"net/url"
	"strings"

	"github.com/snyk/go-application-framework/internal/constants"
	"github.com/snyk/go-application-framework/internal/utils"
	"github.com/snyk/go-application-framework/pkg/configuration"
	localworkflows "github.com/snyk/go-application-framework/pkg/local_workflows"
	"github.com/snyk/go-application-framework/pkg/networking"
	"github.com/snyk/go-application-framework/pkg/workflow"
	"github.com/snyk/go-httpauth/pkg/httpauth"
)

func initConfiguration(config configuration.Configuration) {
	dir, _ := utils.SnykCacheDir()

	config.AddDefaultValue(configuration.ANALYTICS_DISABLED, configuration.StandardDefaultValueFunction(false))
	config.AddDefaultValue(configuration.WORKFLOW_USE_STDIO, configuration.StandardDefaultValueFunction(false))
	config.AddDefaultValue(configuration.PROXY_AUTHENTICATION_MECHANISM, configuration.StandardDefaultValueFunction(httpauth.StringFromAuthenticationMechanism(httpauth.AnyAuth)))
	config.AddDefaultValue(configuration.DEBUG_FORMAT, configuration.StandardDefaultValueFunction(log.Ldate|log.Ltime|log.Lmicroseconds|log.Lmsgprefix|log.LUTC))
	config.AddDefaultValue(configuration.CACHE_PATH, configuration.StandardDefaultValueFunction(dir))

	config.AddDefaultValue(configuration.API_URL, func(existingValue interface{}) interface{} {
		if existingValue == nil {
			return constants.SNYK_DEFAULT_API_URL
		} else {
			apiString := existingValue
			if temp, ok := existingValue.(string); ok {
				if apiUrl, err := url.Parse(temp); err == nil {
					apiUrl.Path = strings.Replace(apiUrl.Path, "/v1", "", 1)
					apiString = apiUrl.String()
				}
			}
			return apiString
		}
	})

	config.AddDefaultValue(configuration.ORGANIZATION, func(existingValue interface{}) interface{} {
		useDefaultValue := true
		orgid := existingValue
		if existingValue != nil {
			if len(existingValue.(string)) > 0 {
				useDefaultValue = false
			}
		}

		if useDefaultValue {
			api := config.GetString(configuration.API_URL)
			client := networking.NewNetworkAccess(config).GetHttpClient()
			orgid, _ = utils.GetDefaultOrgID(client, api)
			config.Set(configuration.ORGANIZATION, orgid)
		}

		return orgid
	})

	config.AddAlternativeKeys(configuration.AUTHENTICATION_TOKEN, []string{"snyk_token", "snyk_cfg_api", "api"})
	config.AddAlternativeKeys(configuration.AUTHENTICATION_BEARER_TOKEN, []string{"snyk_oauth_token", "snyk_docker_token"})
}

func CreateAppEngine() workflow.Engine {
	config := configuration.New()
	initConfiguration(config)

	engine := workflow.NewWorkFlowEngine(config)

	engine.AddExtensionInitializer(localworkflows.Init)

	return engine
}