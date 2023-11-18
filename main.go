package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

type Pipeline struct {
	Description string   `yaml:"description"`
	BaseURL     *string  `yaml:"baseURL"`
	Actions     []Action `yaml:"actions"`
}

type Action struct {
	Method          *string            `yaml:"method"`
	StatusCode      *int               `yaml:"statusCode"`
	Body            *map[string]any    `yaml:"body"`
	Setters         *map[string]string `yaml:"setters"`
	Description     *string            `yaml:"description"`
	LogResponseBody *bool              `yaml:"logResponseBody"`
	LogRequestBody  *bool              `yaml:"logRequestBody"`
	Endpoint        string             `yaml:"endpoint"`
}

func main() {
	data, err := os.ReadFile("pipeline.yaml")

	if err != nil {
		panic(err)
	}

	var pipeline Pipeline

	err = yaml.Unmarshal(data, &pipeline)

	if err != nil {
		panic(err)
	}

	logger := NewLogger()

	logger.green("Apiline")
	logger.green("=================================")
	logger.magenta(pipeline.Description)
	logger.green()
	logger = logger.withPrefix(Green + "|| " + Reset)

	client := &http.Client{}

	variables := make(map[string]any)

	for _, action := range pipeline.Actions {

		method := valueIfNil(action.Method, "GET")

		description := valueIfNil(action.Description, "")

		logger.magenta(method + " " + description)

		endPoint := action.Endpoint

		if pipeline.BaseURL != nil {
			endPoint = *pipeline.BaseURL + "/" + endPoint
		}

		endPoint = replaceVariablesOnString(endPoint, variables).(string)

		logger.blue(endPoint)

		var body any

		if action.Body != nil {
			convertedMap := make(map[string]any)
			for k, v := range *action.Body {
				convertedMap[k] = v
			}

			body = replaceVariablesOnMap(
				convertedMap,
				variables,
			)
		}

		jsonData, err := json.Marshal(body)

		if err != nil {
			logger.red("Error marshaling BODY")
			logger.red(err)
			logger.red("Body:")
			logger.red(body)
		}

		if action.Body != nil && action.LogRequestBody != nil && *action.LogRequestBody {
			logger.blue("BODY:")
			logger.blue(string(jsonData))
		}

		req, err := http.NewRequest(method, endPoint, bytes.NewBuffer(jsonData))

		if err != nil {
			logger.red("Error creating request: ", err)
			os.Exit(1)
		}

		resp, err := client.Do(req)

		if err != nil {
			logger.red("Error sending request: ", err)
			os.Exit(1)
		}

		defer resp.Body.Close()

		expectedStatusCode := valueIfNil(action.StatusCode, 200)

		if resp.StatusCode != expectedStatusCode {
			logger.red("Expected status code ", expectedStatusCode, " but got ", resp.StatusCode)

			body, err := io.ReadAll(resp.Body)

			if err != nil {
				logger.red("Error reading response body: ", err)
				os.Exit(1)
			}

			if len(body) == 0 {
				logger.red("Response:")
				logger.red("Empty")
				continue
			}

			var responseBody map[string]any

			if err := json.Unmarshal(body, &responseBody); err != nil {
				logger.red("Error unmarshaling response: ", err)
				os.Exit(1)
			}

			logger.red("Response:")
			logger.red(responseBody)
			os.Exit(1)
		}

		logger.green("GOT: ", resp.StatusCode)

		if action.Setters != nil {
			logger.blue("Setting variables")

			for key, value := range *action.Setters {

				data, err := extractDataFromResponse(resp, key)

				if err != nil {
					logger.red("Error extracting data from response")
					logger.red(err)
					logger.red("Key:")
					logger.red(key)
					os.Exit(1)
				}

				variables[value] = data

				logger.blue(value, " -> ", data)
			}
		}

		if action.LogResponseBody != nil && *action.LogResponseBody {
			body, err := io.ReadAll(resp.Body)

			if err != nil {
				logger.red("Error reading response body: ", err)
				os.Exit(1)
			}

			if len(body) == 0 {
				logger.blue("Response:")
				logger.blue("Empty")
				continue
			}

			var responseBody map[string]any

			if err := json.Unmarshal(body, &responseBody); err != nil {
				logger.red("Error unmarshaling response: ", err)
				os.Exit(1)
			}

			logger.blue("Response:")
			logger.blue(responseBody)
		}

		logger.green()
	}

	logger.green("=================================")
}

func valueIfNil[T any](target *T, value T) T {
	if target == nil {
		return value
	}
	return *target
}

func replaceVariablesOnMap(data, variables map[string]any) map[string]any {
	parsed := make(map[string]any)

	for key, value := range data {
		parsed[key] = replaceVariablesOnString(value, variables)
	}

	return parsed
}

func replaceVariablesOnString(data any, variables map[string]any) any {
	// TODO: Add support for non strings
	switch d := data.(type) {
	case string:
		pattern := regexp.MustCompile(`@\{[^}]*\}`)
		return pattern.ReplaceAllStringFunc(d, func(match string) string {
			placeholder := strings.Trim(match, "@{}")
			if val, ok := variables[placeholder]; ok {
				if strVal, ok := val.(string); ok {
					return strVal
				}
			}
			return match
		})
	default:
		// If data is not a string, return as is
		return data
	}
}

func extractDataFromResponse(resp *http.Response, path string) (any, error) {
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	keys := strings.Split(path, "/")
	var current any = body

	for _, key := range keys {
		switch {
		case strings.HasPrefix(key, "[") && strings.HasSuffix(key, "]"):
			indexStr := key[1 : len(key)-1]
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				return nil, fmt.Errorf("invalid array index: %s", indexStr)
			}

			if currentSlice, ok := current.([]any); ok {
				if index < 0 || index >= len(currentSlice) {
					return nil, errors.New("array index out of range")
				}
				current = currentSlice[index]
			} else {
				return nil, errors.New("current value is not an array")
			}

		default:
			if currentMap, ok := current.(map[string]any); ok {
				current = currentMap[key]
			} else {
				return nil, fmt.Errorf("key not found: %s", key)
			}
		}
	}

	return current, nil
}

const (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Bold    = "\033[1m"
)

type Logger struct {
	Prefix *string
}

func NewLogger() *Logger {
	return &Logger{}
}

func (l *Logger) withPrefix(prefix string) *Logger {
	l.Prefix = &prefix
	return l
}

func (l *Logger) green(msg ...any) {
	fmt.Print(valueIfNil(l.Prefix, ""))
	fmt.Print(Green)
	fmt.Print(msg...)
	fmt.Println(Reset)
}

func (l *Logger) red(msg ...any) {
	fmt.Print(valueIfNil(l.Prefix, ""))
	fmt.Print(Red)
	fmt.Print(msg...)
	fmt.Println(Reset)
}

func (l *Logger) blue(msg ...any) {
	fmt.Print(valueIfNil(l.Prefix, ""))
	fmt.Print(Blue)
	fmt.Print(msg...)
	fmt.Println(Reset)
}

func (l *Logger) magenta(msg ...any) {
	fmt.Print(valueIfNil(l.Prefix, ""))
	fmt.Print(Magenta)
	fmt.Print(msg...)
	fmt.Println(Reset)
}
