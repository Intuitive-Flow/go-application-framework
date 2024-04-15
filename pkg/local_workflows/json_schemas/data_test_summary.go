package json_schemas

const TestSummarySchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Test Results",
  "description": "Schema for security test results with severity and counts",
  "type": "object",
  "required": ["results", "type"],
  "properties": {
    "results": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["severity", "total", "open", "ignored"],
        "properties": {
          "severity": {
            "type": "string",
            "enum": ["high", "medium", "low", "critical"]
          },
          "total": {
            "type": "integer",
            "minimum": 0
          },
          "open": {
            "type": "integer",
            "minimum": 0
          },
          "ignored": {
            "type": "integer",
            "minimum": 0
          }
        }
      }
    },
    "type": {
      "type": "string",
      "enum": ["sast", "sbom"]
    },
    "severity_order_asc": {
      "type": "array",
      "items": {
        "type": "string"
      }
    }
  }
}`

type TestSummaryResult struct {
	Severity string `json:"severity"`
	Total    int    `json:"total"`
	Open     int    `json:"open"`
	Ignored  int    `json:"ignored"`
}

type TestSummary struct {
	Results          []TestSummaryResult `json:"results"`
	SeverityOrderAsc []string            `json:"severity_order_asc,omitempty"`
	Type             string              `json:"type"`
}

func NewTestSummary(t string) *TestSummary {
	return &TestSummary{
		Type:             t,
		SeverityOrderAsc: []string{"low", "medium", "high", "critical"},
	}
}
