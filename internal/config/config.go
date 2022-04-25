package config

import (
	"encoding/csv"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/yohamta/jobctl/internal/constants"
	"github.com/yohamta/jobctl/internal/settings"
	"github.com/yohamta/jobctl/internal/utils"
)

type Config struct {
	ConfigPath        string
	Name              string
	Description       string
	Env               []string
	LogDir            string
	HandlerOn         HandlerOn
	Steps             []*Step
	MailOn            MailOn
	ErrorMail         *MailConfig
	InfoMail          *MailConfig
	Smtp              *SmtpConfig
	DelaySec          time.Duration
	HistRetentionDays int
	Preconditions     []*Condition
	MaxActiveRuns     int
	Params            []string
	DefaultParams     string
}

type HandlerOn struct {
	Failure *Step
	Success *Step
	Cancel  *Step
	Exit    *Step
}

type MailOn struct {
	Failure bool
	Success bool
}

func ReadConfig(file string) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *Config) Init() {
	if c.Env == nil {
		c.Env = []string{}
	}
	if c.Steps == nil {
		c.Steps = []*Step{}
	}
	if c.Params == nil {
		c.Params = []string{}
	}
	if c.Preconditions == nil {
		c.Preconditions = []*Condition{}
	}
}

func (c *Config) setup(file string) {
	c.ConfigPath = file
	if c.LogDir == "" {
		c.LogDir = path.Join(
			settings.MustGet(settings.CONFIG__LOGS_DIR),
			utils.ValidFilename(c.Name, "_"))
	}
	if c.HistRetentionDays == 0 {
		c.HistRetentionDays = 7
	}
	dir := path.Dir(file)
	for _, step := range c.Steps {
		c.setupStep(step, dir)
	}
	if c.HandlerOn.Exit != nil {
		c.setupStep(c.HandlerOn.Exit, dir)
	}
	if c.HandlerOn.Success != nil {
		c.setupStep(c.HandlerOn.Success, dir)
	}
	if c.HandlerOn.Failure != nil {
		c.setupStep(c.HandlerOn.Failure, dir)
	}
	if c.HandlerOn.Cancel != nil {
		c.setupStep(c.HandlerOn.Cancel, dir)
	}
}

func (c *Config) setupStep(step *Step, defaultDir string) {
	if step.Dir == "" {
		step.Dir = path.Dir(c.ConfigPath)
	}
}

func (c *Config) Clone() *Config {
	ret := *c
	return &ret
}

func (c *Config) String() string {
	ret := fmt.Sprintf("{\n")
	ret = fmt.Sprintf("%s\tName: %s\n", ret, c.Name)
	ret = fmt.Sprintf("%s\tDescription: %s\n", ret, strings.TrimSpace(c.Description))
	ret = fmt.Sprintf("%s\tEnv: %v\n", ret, strings.Join(c.Env, ", "))
	ret = fmt.Sprintf("%s\tLogDir: %v\n", ret, c.LogDir)
	for i, s := range c.Steps {
		ret = fmt.Sprintf("%s\tStep%d: %v\n", ret, i, s)
	}
	ret = fmt.Sprintf("%s}\n", ret)
	return ret
}

type BuildConfigOptions struct {
	headOnly   bool
	parameters string
}

func buildFromDefinition(def *configDefinition, file string, globalConfig *Config,
	opts *BuildConfigOptions) (c *Config, err error) {
	c = &Config{}
	c.Init()

	c.Name = def.Name
	c.Description = def.Description
	c.MailOn.Failure = def.MailOn.Failure
	c.MailOn.Success = def.MailOn.Success
	c.DelaySec = time.Second * time.Duration(def.DelaySec)

	if opts != nil && opts.headOnly {
		return c, nil
	}

	env, err := loadVariables(def.Env)
	if err != nil {
		return nil, err
	}

	c.Env = buildConfigEnv(env)
	if globalConfig != nil {
		for _, e := range globalConfig.Env {
			key := strings.SplitN(e, "=", 2)[0]
			if _, ok := env[key]; !ok {
				c.Env = append(c.Env, e)
			}
		}
	}

	logDir, err := utils.ParseVariable(def.LogDir)
	if err != nil {
		return nil, err
	}
	c.LogDir = logDir
	if def.HistRetentionDays != nil {
		c.HistRetentionDays = *def.HistRetentionDays
	}

	c.DefaultParams = def.Params
	if opts.parameters != "" {
		c.Params, err = parseParameters(opts.parameters, false)
		if err != nil {
			return nil, err
		}
	} else {
		c.Params, err = parseParameters(c.DefaultParams, true)
		if err != nil {
			return nil, err
		}
	}

	c.Steps, err = buildStepsFromDefinition(c.Env, def.Steps)
	if err != nil {
		return nil, err
	}

	if def.HandlerOn.Exit != nil {
		def.HandlerOn.Exit.Name = constants.OnExit
		c.HandlerOn.Exit, err = buildStep(c.Env, def.HandlerOn.Exit)
		if err != nil {
			return nil, err
		}
	}

	if def.HandlerOn.Success != nil {
		def.HandlerOn.Success.Name = constants.OnSuccess
		c.HandlerOn.Success, err = buildStep(c.Env, def.HandlerOn.Success)
		if err != nil {
			return nil, err
		}
	}

	if def.HandlerOn.Failure != nil {
		def.HandlerOn.Failure.Name = constants.OnFailure
		c.HandlerOn.Failure, err = buildStep(c.Env, def.HandlerOn.Failure)
		if err != nil {
			return nil, err
		}
	}

	if def.HandlerOn.Cancel != nil {
		def.HandlerOn.Cancel.Name = constants.OnCancel
		c.HandlerOn.Cancel, err = buildStep(c.Env, def.HandlerOn.Cancel)
		if err != nil {
			return nil, err
		}
	}

	c.Smtp, err = buildSmtpConfigFromDefinition(def.Smtp)
	if err != nil {
		return nil, err
	}
	c.ErrorMail, err = buildMailConfigFromDefinition(def.ErrorMail)
	if err != nil {
		return nil, err
	}
	c.InfoMail, err = buildMailConfigFromDefinition(def.InfoMail)
	if err != nil {
		return nil, err
	}
	c.Preconditions = loadPreCondition(def.Preconditions)
	c.MaxActiveRuns = def.MaxActiveRuns

	return c, nil
}

func parseParameters(value string, eval bool) ([]string, error) {
	params := value
	var err error
	if eval {
		params, err = utils.ParseCommand(os.ExpandEnv(value))
		if err != nil {
			return nil, err
		}
	}
	r := csv.NewReader(strings.NewReader(params))
	r.Comma = ' '
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	ret := []string{}
	for _, r := range records {
		for i, v := range r {
			err = os.Setenv(strconv.Itoa(i+1), v)
			if err != nil {
				return nil, err
			}
			ret = append(ret, v)
		}
	}
	return ret, nil
}

func buildSmtpConfigFromDefinition(def smtpConfigDef) (*SmtpConfig, error) {
	smtp := &SmtpConfig{}
	smtp.Host = def.Host
	smtp.Port = def.Port
	return smtp, nil
}

func buildMailConfigFromDefinition(def mailConfigDef) (*MailConfig, error) {
	c := &MailConfig{}
	c.From = def.From
	c.To = def.To
	c.Prefix = def.Prefix
	return c, nil
}

func buildStepsFromDefinition(variables []string, stepDefs []*stepDef) ([]*Step, error) {
	ret := []*Step{}
	for _, def := range stepDefs {
		step, err := buildStep(variables, def)
		if err != nil {
			return nil, err
		}
		ret = append(ret, step)
	}
	return ret, nil
}

func buildStep(variables []string, def *stepDef) (*Step, error) {
	if err := assertStepDef(def); err != nil {
		return nil, err
	}
	step := &Step{}
	step.Name = def.Name
	step.Description = def.Description
	step.Command, step.Args = utils.SplitCommand(def.Command)
	step.Dir = os.ExpandEnv(def.Dir)
	step.Variables = variables
	step.Depends = def.Depends
	if def.ContinueOn != nil {
		step.ContinueOn.Skipped = def.ContinueOn.Skipped
		step.ContinueOn.Failure = def.ContinueOn.Failure
	}
	if def.RetryPolicy != nil {
		step.RetryPolicy = &RetryPolicy{
			Limit: def.RetryPolicy.Limit,
		}
	}
	step.MailOnError = def.MailOnError
	step.Repeat = def.Repeat
	step.RepeatInterval = time.Second * time.Duration(def.RepeatIntervalSec)
	step.Preconditions = loadPreCondition(def.Preconditions)
	return step, nil
}

func buildConfigEnv(vars map[string]string) []string {
	ret := []string{}
	for k, v := range vars {
		ret = append(ret, fmt.Sprintf("%s=%s", k, v))
	}
	return ret
}

func loadPreCondition(cond []*conditionDef) []*Condition {
	ret := []*Condition{}
	for _, v := range cond {
		ret = append(ret, &Condition{
			Condition: v.Condition,
			Expected:  v.Expected,
		})
	}
	return ret
}

func loadVariables(strVariables map[string]string) (map[string]string, error) {
	vars := map[string]string{}
	for k, v := range strVariables {
		parsed, err := utils.ParseVariable(v)
		if err != nil {
			return nil, err
		}
		vars[k] = parsed
		err = os.Setenv(k, parsed)
		if err != nil {
			return nil, err
		}
	}
	return vars, nil
}

func assertDef(def *configDefinition) error {
	if def.Name == "" {
		return fmt.Errorf("job name must be specified.")
	}
	if len(def.Steps) == 0 {
		return fmt.Errorf("at least one step must be specified.")
	}
	return nil
}

func assertStepDef(def *stepDef) error {
	if def.Name == "" {
		return fmt.Errorf("step name must be specified.")
	}
	if def.Command == "" {
		return fmt.Errorf("step command must be specified.")
	}
	return nil
}