// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package wstesting

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/gherkin-go"

	"github.com/helloeave/worksheets"
)

type command interface {
	run(ctx *Context) error
}

// Assert all commands implement the command interface.
var _ = []command{
	cLoad{},
	cCreate{},
	cSet{},
	cAppend{},
	cDel{},
	cAssert{},
}

type cLoad struct {
	filename string
}

type cCreate struct {
	ws, name string
}

type cSet struct {
	ws     string
	values map[string]worksheets.Value
}

type cAppend struct {
	ws, field string
	values    []worksheets.Value
}

type cDel struct {
	ws, field string
	indexes   []int
}

type cAssert struct {
	ws       string
	partial  bool
	expected map[string]worksheets.Value
}

func stepToCommand(step *gherkin.Step) (command, error) {
	parts := strings.Split(strings.TrimSpace(step.Text), " ")
	switch parts[0] {
	case "load":
		if len(parts) != 2 {
			return nil, fmt.Errorf(`%s: expecting load "<filename>"`, step.Text)
		}
		filename, err := strconv.Unquote(parts[1])
		if err != nil {
			return nil, fmt.Errorf(`%s: expecting quoted filename, e.g. "my_definitions.ws"`, step.Text)
		}
		return cLoad{filename}, nil
	case "create":
		if len(parts) != 3 {
			return nil, fmt.Errorf(`%s: expecting create <ws> "<name>"`, step.Text)
		}
		name, err := strconv.Unquote(parts[2])
		if err != nil {
			return nil, fmt.Errorf(`%s: expecting quoted name, e.g. "my_name"`, step.Text)
		}
		return cCreate{parts[1], name}, nil
	case "set":
		var set cSet
		switch len(parts) {
		case 2:
			set.ws = parts[1]
			values, partial, err := tableToContents(step.Argument)
			if err != nil {
				if _, _, ok := splitWsAndField(parts[1]); ok && step.Argument == nil {
					return nil, fmt.Errorf("%s: missing value", step.Text)
				}
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			if partial {
				return nil, fmt.Errorf("%s: partial not allowed", step.Text)
			}
			set.values = values
		case 3:
			ws, field, ok := splitWsAndField(parts[1])
			if !ok {
				return nil, fmt.Errorf("%s: expecting <ws>.<field>", step.Text)
			}
			set.ws = ws
			value, err := worksheets.NewValue(parts[2])
			if err != nil {
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			set.values = map[string]worksheets.Value{
				field: value,
			}
		default:
			return nil, fmt.Errorf("%s: expecting <ws> with data table or <ws.field> with value", step.Text)
		}
		return set, nil
	case "unset":
		if len(parts) != 2 {
			return nil, fmt.Errorf("%s: expecting <ws> with field table or <ws.field>", step.Text)
		}
		var set cSet
		if step.Argument != nil {
			set.ws = parts[1]
			fields, err := tableToFields(step.Argument)
			if err != nil {
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			set.values = make(map[string]worksheets.Value)
			for _, field := range fields {
				set.values[field] = worksheets.NewUndefined()
			}
		} else {
			ws, field, ok := splitWsAndField(parts[1])
			if !ok {
				return nil, fmt.Errorf("%s: expecting <ws>.<field>", step.Text)
			}
			set.ws = ws
			set.values = map[string]worksheets.Value{
				field: worksheets.NewUndefined(),
			}
		}
		return set, nil
	case "append":
		if len(parts) < 2 || 3 < len(parts) {
			return nil, fmt.Errorf("%s: expecting <ws>.<field> with value or value table", step.Text)
		}
		var app cAppend
		ws, field, ok := splitWsAndField(parts[1])
		if !ok {
			return nil, fmt.Errorf("%s: expecting <ws>.<field>", step.Text)
		}
		app.ws = ws
		app.field = field
		switch len(parts) {
		case 2:
			values, err := tableToValues(step.Argument)
			if err != nil {
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			app.values = values
		case 3:
			value, err := worksheets.NewValue(parts[2])
			if err != nil {
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			app.values = []worksheets.Value{
				value,
			}
		}
		return app, nil
	case "del":
		var del cDel
		switch len(parts) {
		case 2:
			ws, field, ok := splitWsAndField(parts[1])
			if !ok {
				return nil, fmt.Errorf("%s: expecting <ws>.<field>", step.Text)
			}
			del.ws = ws
			del.field = field
			indexes, err := tableToIndexes(step.Argument)
			if err != nil {
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			del.indexes = indexes
		case 3:
			ws, field, ok := splitWsAndField(parts[1])
			if !ok {
				return nil, fmt.Errorf("%s: expecting <ws>.<field>", step.Text)
			}
			del.ws = ws
			del.field = field
			index, err := strconv.Atoi(parts[2])
			if err != nil {
				return nil, fmt.Errorf("%s: unreadable index %s", step.Text, parts[2])
			}
			del.indexes = []int{
				index,
			}
		default:
			return nil, fmt.Errorf("%s: expecting <ws>.<field> with index or index table", step.Text)
		}
		return del, nil
	case "assert":
		var assert cAssert
		switch len(parts) {
		case 2:
			assert.ws = parts[1]
			values, partial, err := tableToContents(step.Argument)
			if err != nil {
				if _, _, ok := splitWsAndField(parts[1]); ok && step.Argument == nil {
					return nil, fmt.Errorf("%s: missing value", step.Text)
				}
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			assert.partial = partial
			assert.expected = values
		case 3:
			ws, field, ok := splitWsAndField(parts[1])
			if !ok {
				return nil, fmt.Errorf("%s: expecting <ws>.<field>", step.Text)
			}
			assert.ws = ws
			assert.partial = true
			value, err := worksheets.NewValue(parts[2])
			if err != nil {
				return nil, fmt.Errorf("%s: %s", step.Text, err)
			}
			assert.expected = map[string]worksheets.Value{
				field: value,
			}
		default:
			return nil, fmt.Errorf("%s: expecting <ws> with data table or <ws.field> with value", step.Text)
		}
		return assert, nil
	default:
		if parts[0] == "" {
			return nil, fmt.Errorf("no verb: expecting verb load, create, set, unset, append, del, or assert")
		} else {
			return nil, fmt.Errorf("wrong verb '%s': expecting verb load, create, set, unset, append, del, or assert", parts[0])
		}
	}
}

func splitWsAndField(wsAndField string) (string, string, bool) {
	parts := strings.SplitN(wsAndField, ".", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (cmd cLoad) run(ctx *Context) error {
	if ctx.Defs != nil {
		return fmt.Errorf("cannot provide multiple definitions files")
	}

	defsFile, err := os.Open(filepath.Join(ctx.CurrentDir, cmd.filename))
	if err != nil {
		return err
	}
	defer defsFile.Close()

	reader := bufio.NewReader(defsFile)
	defs, err := worksheets.NewDefinitions(reader)
	if err != nil {
		return err
	}

	ctx.Defs = defs

	return nil
}

func (cmd cCreate) run(ctx *Context) error {
	if ctx.Defs == nil {
		return fmt.Errorf("must first load definitions file")
	}
	if _, ok := ctx.sheets[cmd.ws]; ok {
		return fmt.Errorf("worksheet %s already created", cmd.ws)
	}

	ws, err := ctx.Defs.NewWorksheet(cmd.name)
	if err != nil {
		return err
	}

	ctx.sheets[cmd.ws] = ws
	return nil
}

func (cmd cSet) run(ctx *Context) error {
	ws, ok := ctx.sheets[cmd.ws]
	if !ok {
		return fmt.Errorf("worksheet %s not yet created", cmd.ws)
	}
	for field, value := range cmd.values {
		if err := ws.Set(field, value); err != nil {
			return err
		}
	}
	return nil
}

func (cmd cAppend) run(ctx *Context) error {
	ws, ok := ctx.sheets[cmd.ws]
	if !ok {
		return fmt.Errorf("worksheet %s not yet created", cmd.ws)
	}
	for _, value := range cmd.values {
		if err := ws.Append(cmd.field, value); err != nil {
			return err
		}
	}
	return nil
}

func (cmd cDel) run(ctx *Context) error {
	ws, ok := ctx.sheets[cmd.ws]
	if !ok {
		return fmt.Errorf("worksheet %s not yet created", cmd.ws)
	}
	for _, index := range cmd.indexes {
		if err := ws.Del(cmd.field, index); err != nil {
			return err
		}
	}
	return nil
}

func (cmd cAssert) run(ctx *Context) error {
	ws, ok := ctx.sheets[cmd.ws]
	if !ok {
		return fmt.Errorf("worksheet %s not yet created", cmd.ws)
	}
	var diffs []string
	for field, expected := range cmd.expected {
		actual, err := ws.Get(field)
		if err != nil {
			return err
		}
		if !expected.Equal(actual) {
			diffs = append(diffs, fmt.Sprintf("%s: expected <%s>, was <%s>", field, expected, actual))
		}
	}
	if !cmd.partial {
		def := ws.Type().(*worksheets.Definition)
		for _, field := range def.Fields() {
			name := field.Name()
			if name == "version" || name == "id" {
				continue
			}
			if _, alreadyChecked := cmd.expected[field.Name()]; alreadyChecked {
				continue
			}
			expected := worksheets.NewUndefined()
			actual, err := ws.Get(name)
			if err != nil {
				return err
			}
			if !expected.Equal(actual) {
				diffs = append(diffs, fmt.Sprintf("%s: expected <%s>, was <%s>", name, expected, actual))
			}
		}
	}
	if len(diffs) != 0 {
		return fmt.Errorf(strings.Join(diffs, "\n"))
	}
	return nil
}

// Context holds all that is necessery to run a scenario.
type Context struct {
	// CurrentDir is the current working directory when resolving relative path
	// names contained in the scenario.
	CurrentDir string

	// Defs are the worksheet definitions used when running the scenarions. In
	// the case where plugins are required, the definitions must be provided
	// directly via the context rather than relying solely on loading definitions
	// from a ws definition file.
	Defs *worksheets.Definitions

	// sheets are the worksheets defined as the scenario is running. Since this
	// map is modified during scenario execution, it is strongly suggested to
	// provide `nil`, or to provide a fresh copy for each and every scenario
	// run.
	sheets map[string]*worksheets.Worksheet
}

// Scenario represents a single scenario from a .feature.
type Scenario struct {
	// Name is the scenario's name.
	Name string

	steps    []*gherkin.Step
	commands []command
}

// Run runs the scenario using the provided context.
func (s Scenario) Run(ctx Context) error {
	ctx.sheets = make(map[string]*worksheets.Worksheet)
	for i, cmd := range s.commands {
		if err := cmd.run(&ctx); err != nil {
			return fmt.Errorf("%s: %s", s.steps[i].Text, err)
		}
	}
	return nil
}

// ReadFeature reads a feature in gherkin syntax, and parses out all the
// scenarios contained herein.
func ReadFeature(reader io.Reader) ([]Scenario, error) {
	doc, err := gherkin.ParseGherkinDocument(reader)
	if err != nil {
		return nil, err
	}

	scenarios, err := docToScenarios(doc)
	if err != nil {
		return nil, err
	}

	return scenarios, nil
}

// RunFeature runs a feature test.
func RunFeature(t *testing.T, filename string, opts ...Context) {
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}

	scenarios, err := ReadFeature(bufio.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}

	// context
	var ctx Context
	switch len(opts) {
	case 0:
		ctx.CurrentDir = filepath.Dir(filename)
	case 1:
		ctx = opts[0]
	default:
		t.Fatalf("too many contexts provided")
	}

	// run scenarios
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			err := scenario.Run(ctx)
			if err != nil {
				t.Error(err)
			}
		})
	}
}

func docToScenarios(doc *gherkin.GherkinDocument) ([]Scenario, error) {
	var (
		bgSteps    []*gherkin.Step
		bgCommands []command
		scenarios  []Scenario
	)
	for _, untypedChild := range doc.Feature.Children {
		switch child := untypedChild.(type) {
		case *gherkin.Scenario:
			var commands []command
			for _, step := range child.Steps {
				cmd, err := stepToCommand(step)
				if err != nil {
					return nil, err
				}
				commands = append(commands, cmd)
			}
			scenarios = append(scenarios, Scenario{
				Name:     child.Name,
				steps:    child.Steps,
				commands: commands,
			})
		case *gherkin.Background:
			for _, step := range child.Steps {
				cmd, err := stepToCommand(step)
				if err != nil {
					return nil, err
				}
				bgCommands = append(bgCommands, cmd)
			}
			bgSteps = child.Steps
		default:
			return nil, fmt.Errorf("unknwon child type %T\n", child)
		}
	}
	for i := range scenarios {
		scenarios[i].steps = append(bgSteps, scenarios[i].steps...)
		scenarios[i].commands = append(bgCommands, scenarios[i].commands...)
	}
	return scenarios, nil
}

func tableToContents(extra interface{}) (map[string]worksheets.Value, bool, error) {
	table, ok := extra.(*gherkin.DataTable)
	if !ok {
		return nil, false, fmt.Errorf("must provide a data table")
	}

	contents := make(map[string]worksheets.Value)
	partial := false
	for _, row := range table.Rows {
		if len(row.Cells) != 2 {
			return nil, false, fmt.Errorf("must provide a table with two columns on every row")
		}
		key := row.Cells[0].Value
		if strings.TrimSpace(key) == "-" {
			partial = true
			continue
		}
		value, err := worksheets.NewValue(row.Cells[1].Value)
		if err != nil {
			return nil, false, err
		}
		contents[key] = value
	}

	return contents, partial, nil
}

func tableToIndexes(extra interface{}) ([]int, error) {
	table, ok := extra.(*gherkin.DataTable)
	if !ok {
		return nil, fmt.Errorf("must provide an index table")
	}

	var indexes []int
	for _, row := range table.Rows {
		if len(row.Cells) != 1 {
			return nil, fmt.Errorf("must provide a table with one column on every row")
		}
		index, err := strconv.Atoi(row.Cells[0].Value)
		if err != nil {
			return nil, fmt.Errorf("unreadable index %s", row.Cells[0].Value)
		}
		indexes = append(indexes, index)
	}

	return indexes, nil
}

func tableToFields(extra interface{}) ([]string, error) {
	table, ok := extra.(*gherkin.DataTable)
	if !ok {
		return nil, fmt.Errorf("must provide a field table")
	}

	var fields []string
	for _, row := range table.Rows {
		if len(row.Cells) != 1 {
			return nil, fmt.Errorf("must provide a table with one column on every row")
		}
		fields = append(fields, row.Cells[0].Value)
	}

	return fields, nil
}

func tableToValues(extra interface{}) ([]worksheets.Value, error) {
	table, ok := extra.(*gherkin.DataTable)
	if !ok {
		return nil, fmt.Errorf("must provide a value table")
	}

	var values []worksheets.Value
	for _, row := range table.Rows {
		if len(row.Cells) != 1 {
			return nil, fmt.Errorf("must provide a table with one column on every row")
		}
		value, err := worksheets.NewValue(row.Cells[0].Value)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}

	return values, nil
}
