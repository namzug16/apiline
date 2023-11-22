package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Pipeline struct {
	Description string   `json:"description"`
	BaseURL     *string  `json:"baseURL"`
	Actions     []Action `json:"actions"`
}

type Action struct {
	Method          *string            `json:"method"`
	StatusCode      *int               `json:"statusCode"`
	Body            *map[string]any    `json:"body"`
	Setters         *map[string]string `json:"setters"`
	Assertions      *map[string]any    `json:"assertions"`
	Description     *string            `json:"description"`
	LogResponseBody *bool              `json:"log_response_body"`
	LogRequestBody  *bool              `json:"log_request_body"`
	Endpoint        string             `json:"endpoint"`
}

func main() {
	files := filesToExecute()

	logger := NewLogger()

	logger.cyan(
		`
     ________      ________    ___      ___           ___      ________       _______      
    |\   __  \    |\   __  \  |\  \    |\  \         |\  \    |\   ___  \    |\  ___ \     
    \ \  \|\  \   \ \  \|\  \ \ \  \   \ \  \        \ \  \   \ \  \\ \  \   \ \   __/|    
     \ \   __  \   \ \   ____\ \ \  \   \ \  \        \ \  \   \ \  \\ \  \   \ \  \_|/__  
      \ \  \ \  \   \ \  \___|  \ \  \   \ \  \____    \ \  \   \ \  \\ \  \   \ \  \_|\ \ 
       \ \__\ \__\   \ \__\      \ \__\   \ \_______\   \ \__\   \ \__\\ \__\   \ \_______\
        \|__|\|__|    \|__|       \|__|    \|_______|    \|__|    \|__| \|__|    \|_______|
    `,
	)

	logger.blue("Files to execute: ", files)

	for _, file := range files {
		logger.green()
		logger.green()
		logger.blue("FILE: ", file)
		err := RunPipeline(logger, file)
		if err != nil {
			logger.red("Error running pipeline")
			logger.red(err)
			break
		}
		logger = logger.resetPrefix()
	}

	logger.green()
}

func filesToExecute() []string {

	files := make([]string, 0)

	if len(os.Args) < 2 {
		fmt.Println("Usage: apiline <file|directory> or file1.yaml file2.yaml")
		os.Exit(1)
	}

	arg := os.Args[1]
	if arg == "." {
		files = append(files, filesOfCurrentDirectory()...)
	} else {
		files = append(files, filesFromArgs(os.Args[1:])...)
	}

	return files
}

func isJsonFile(path string) bool {
	return strings.HasSuffix(path, ".json")
}

func filesOfCurrentDirectory() []string {
	files := make([]string, 0)

	filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("Error accessing path %s: %v\n", path, err)
		}
		if !info.IsDir() {
			if isJsonFile(path) {
				files = append(files, path)
			}
		}
		return nil
	})

	return files
}

func filesFromArgs(args []string) []string {
	files := make([]string, 0)

	for _, arg := range args {
		fileInfo, err := os.Stat(arg)
		if err != nil {
			fmt.Printf("Error accessing file %s: %v\n", arg, err)
			continue
		}
		if fileInfo.IsDir() {
			filepath.Walk(arg, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					fmt.Printf("Error accessing path %s: %v\n", path, err)
					return err
				}
				if !info.IsDir() {
					if isJsonFile(path) {
						files = append(files, path)
					}
				}
				return nil
			})
		} else {
			if isJsonFile(arg) {
				files = append(files, arg)
			}
		}
	}

	return files
}

func RunPipeline(logger *Logger, file string) error {

	data, err := os.ReadFile(file)

	if err != nil {
		panic(err)
	}

	var pipeline Pipeline

	err = json.Unmarshal(data, &pipeline)

	if err != nil {
		panic(err)
	}

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
			body = replaceVariablesOnMap(
				*action.Body,
				variables,
			)
		}

		jsonData, err := json.Marshal(body)

		if err != nil {
			logger.red("Error marshaling BODY")
			logger.red(err)
			logger.red("Body:")
			logger.red(body)
			return err
		}

		if action.Body != nil && action.LogRequestBody != nil && *action.LogRequestBody {
			logger.blue("BODY:")
			logger.blue(string(jsonData))
		}

		req, err := http.NewRequest(method, endPoint, bytes.NewBuffer(jsonData))

		if err != nil {
			logger.red("Error creating request: ", err)
			return err
		}

		resp, err := client.Do(req)

		if err != nil {
			logger.red("Error sending request: ", err)
			return err
		}

		defer resp.Body.Close()

		expectedStatusCode := valueIfNil(action.StatusCode, 200)

		if resp.StatusCode != expectedStatusCode {
			logger.red("Expected status code ", expectedStatusCode, " but got ", resp.StatusCode)

			body, err := io.ReadAll(resp.Body)

			if err != nil {
				logger.red("Error reading response body: ", err)
				return err
			}

			if len(body) == 0 {
				logger.red("Response:")
				logger.red("Empty")
				continue
			}

			var responseBody map[string]any

			if err := json.Unmarshal(body, &responseBody); err != nil {
				logger.red("Error unmarshaling response: ", err)
				return err
			}

			logger.red("Response:")
			logger.red(responseBody)
			return err
		}

		responseBodyData, err := io.ReadAll(resp.Body)

		var responseBody map[string]any

		if err := json.Unmarshal(responseBodyData, &responseBody); err != nil {
			logger.red("Error unmarshaling response: ", err)
			return err
		}

		logger.green("GOT: ", resp.StatusCode)

		if action.LogResponseBody != nil && *action.LogResponseBody {

			if err != nil {
				logger.red("Error reading response body: ", err)
				return err
			}

			if len(responseBody) == 0 {
				logger.blue("Response:")
				logger.blue("Empty")
				continue
			}

			logger.blue("Response:")
			logger.blue(responseBody)
		}

		if action.Setters != nil {
			logger.blue("Setting variables")

			for key, value := range *action.Setters {

				data, err := extractDataFromResponse(responseBody, key)

				if err != nil {
					logger.red("Error extracting data from response")
					logger.red(err)
					logger.red("Key:")
					logger.red(key)
					return err
				}

				variables[value] = data

				logger.blue(value, " -> ", data)
			}
		}

		if action.Assertions != nil {
			logger.blue("Asserting response")

			assertions := replaceVariablesOnMap(
				*action.Assertions,
				variables,
			)

			for key, value := range assertions {

				data, err := extractDataFromResponse(responseBody, key)

				if err != nil {
					logger.red("Error extracting data from response")
					logger.red(err)
					logger.red("Key:")
					logger.red(key)
					logger.blue("Response:")
					logger.blue(responseBody)
					return err
				}

				if data != value {
					logger.red(key, " != inserted data")
					logger.red("Expected: ", value)
					logger.red("Got: ", data)
					return err
				}

				logger.green(key, " == inserted data")

			}

		}

		logger.green()
	}

	return nil
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
		parsed[key] = replaceVariablesOnData(value, variables)
	}

	return parsed
}

func replaceVariablesOnData(data any, variables map[string]any) any {
	switch v := data.(type) {
	case map[string]any:
		return replaceVariablesOnMap(v, variables)
	case []any:
		replacedElements := make([]any, 0)
		for _, val := range v {
			replacedElements = append(replacedElements, replaceVariablesOnData(val, variables))
		}
		return replacedElements
	default:
		return replaceVariablesOnString(v, variables)
	}
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

func extractDataFromResponse(body map[string]any, path string) (any, error) {
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
	Cyan    = "\033[36m"
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

func (l *Logger) resetPrefix() *Logger {
	l.Prefix = nil
	return l
}

func (l *Logger) cyan(msg ...any) {
	fmt.Print(valueIfNil(l.Prefix, ""))
	fmt.Print(Cyan)
	fmt.Print(msg...)
	fmt.Println(Reset)
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
