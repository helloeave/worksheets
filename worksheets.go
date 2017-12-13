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

package worksheets

import (
	"fmt"
	"io"
	"strconv"

	"github.com/satori/go.uuid"
)

// Definitions encapsulate one or many worksheet definitions, and is the
// overall entry point into the worksheet framework.
//
// TODO(pascal) make sure Definitions are concurrent access safe!
type Definitions struct {
	// defs holds all worksheet definitions
	defs map[string]*tWorksheet
}

// Worksheet is ... TODO(pascal): documentation binge
type Worksheet struct {
	// def holds the definition of this worksheet.
	def *tWorksheet

	// orig holds the worksheet data as it was when it was initially loaded.
	orig map[int]Value

	// data holds all the worksheet data.
	data map[int]Value
}

const (
	// IndexId is the reserved index to store a worksheet's identifier.
	IndexId = -2

	// IndexVersion is the reserved index to store a worksheet's version.
	IndexVersion = -1
)

type ComputedBy interface {
	Args() []string
	Compute(...Value) Value
}

type Options struct {
	// Plugins is a map of workshet names, to field names, to plugins for
	// externally computed fields.
	Plugins map[string]map[string]ComputedBy
}

// NewDefinitions parses a worksheet definition, and creates a worksheet
// model from it.
func NewDefinitions(reader io.Reader, opts ...Options) (*Definitions, error) {
	if 1 < len(opts) {
		return nil, fmt.Errorf("too many options provided")
	}

	// TODO(pascal): support reading multiple worksheet definitions in one file
	p := newParser(reader)
	defs, err := p.parseWorksheets()
	if err != nil {
		return nil, err
	}
	defs := map[string]*tWorksheet{
		def.name: def,
	}

	if len(opts) == 1 {
		opt := opts[0]
		for name, plugins := range opt.Plugins {
			def, ok := defs[name]
			if !ok {
				return nil, fmt.Errorf("plugins: unknown worksheet(%s)", name)
			}
			def.dependents = make(map[int][]int)
			for fieldName, plugin := range plugins {
				field, ok := def.fieldsByName[fieldName]
				if !ok {
					return nil, fmt.Errorf("plugins: unknown field %s.%s", name, fieldName)
				}
				if _, ok := field.computedBy.(*tExternal); !ok {
					return nil, fmt.Errorf("plugins: field %s.%s not externally defined", name, fieldName)
				}
				args := plugin.Args()
				if len(args) == 0 {
					return nil, fmt.Errorf("plugins: %s.%s plugin has no dependencies", name, fieldName)
				}
				for _, argName := range args {
					dependent, ok := def.fieldsByName[argName]
					if !ok {
						return nil, fmt.Errorf("plugins: %s.%s plugin has incorrect arg %s", name, fieldName, argName)
					}
					if _, ok := def.dependents[dependent.index]; !ok {
						def.dependents[dependent.index] = make([]int, 0)
					}
					def.dependents[dependent.index] = append(def.dependents[dependent.index], field.index)

				}
				field.computedBy = &ePlugin{plugin}
			}
		}
	}

	// Any unresolved externals?
	for _, def := range defs {
		for _, field := range def.fields {
			if _, ok := field.computedBy.(*tExternal); ok {
				return nil, fmt.Errorf("plugins: missing plugin for %s.%s", def.name, field.name)
			}
		}
	}

	return &Definitions{
		defs: defs,
	}, nil
}

func (defs *Definitions) MustNewWorksheet(name string) *Worksheet {
	ws, err := defs.NewWorksheet(name)
	if err != nil {
		panic(err)
	}
	return ws
}

func (defs *Definitions) NewWorksheet(name string) (*Worksheet, error) {
	ws, err := defs.newUninitializedWorksheet(name)
	if err != nil {
		return nil, err
	}

	// uuid
	id := uuid.NewV4()
	if err := ws.Set("id", NewText(id.String())); err != nil {
		panic(fmt.Sprintf("unexpected %s", err))
	}

	// version
	if err := ws.Set("version", MustNewValue(strconv.Itoa(1))); err != nil {
		panic(fmt.Sprintf("unexpected %s", err))
	}

	// validate
	if err := ws.validate(); err != nil {
		panic(fmt.Sprintf("unexpected %s", err))
	}

	return ws, nil
}

func (defs *Definitions) newUninitializedWorksheet(name string) (*Worksheet, error) {
	def, ok := defs.defs[name]
	if !ok {
		return nil, fmt.Errorf("unknown worksheet %s", name)
	}

	ws := &Worksheet{
		def:  def,
		orig: make(map[int]Value),
		data: make(map[int]Value),
	}

	return ws, nil
}

func (ws *Worksheet) validate() error {
	// ensure we have an id and a version
	if _, ok := ws.data[IndexId]; !ok {
		return fmt.Errorf("missing id")
	}
	if _, ok := ws.data[IndexVersion]; !ok {
		return fmt.Errorf("missing version")
	}

	// ensure all values are of the proper type
	for index, value := range ws.data {
		field, ok := ws.def.fieldsByIndex[index]
		if !ok {
			return fmt.Errorf("value present for unknown field index %d", index)
		}
		if ok := value.Type().AssignableTo(field.typ); !ok {
			return fmt.Errorf("value present with unassignable type for field index %d", index)
		}
	}

	return nil
}

func (ws *Worksheet) Id() string {
	return ws.data[IndexId].(*tText).value
}

func (ws *Worksheet) Version() int {
	return int(ws.data[IndexVersion].(*tNumber).value)
}

func (ws *Worksheet) Name() string {
	// TODO(pascal): consider having ws.Type().Name() instead
	return ws.def.name
}

func (ws *Worksheet) MustSet(name string, value Value) {
	if err := ws.Set(name, value); err != nil {
		panic(err)
	}
}

func (ws *Worksheet) Set(name string, value Value) error {
	// TODO(pascal): create a 'change', and then commit that change, garantee
	// that commits are atomic, and either win or lose the race by using
	// optimistic concurrency. Change must be a a Definition level, since it
	// could span multiple worksheets at once.

	// lookup field by name
	field, ok := ws.def.fieldsByName[name]
	if !ok {
		return fmt.Errorf("unknown field %s", name)
	}

	// make sure we're not setting a derived field
	// TODO(alex): test and all

	err := ws.set(field, value)
	return err
}

func (ws *Worksheet) set(field *tField, value Value) error {
	index := field.index

	// type check
	litType := value.Type()
	if ok := litType.AssignableTo(field.typ); !ok {
		return fmt.Errorf("cannot assign %s to %s", value, field.typ)
	}

	// store
	if value.Type().AssignableTo(&tUndefinedType{}) {
		delete(ws.data, index)
	} else {
		ws.data[index] = value
	}

	// if this field is an ascendant to any other, recompute them
	for _, dependentIndex := range ws.def.dependents[index] {
		dependent := ws.def.fieldsByIndex[dependentIndex]
		updatedValue := dependent.computedBy.Compute(ws)
		if err := ws.set(dependent, updatedValue); err != nil {
			return err
		}
	}

	return nil
}

func (ws *Worksheet) MustUnset(name string) {
	if err := ws.Unset(name); err != nil {
		panic(err)
	}
}

func (ws *Worksheet) Unset(name string) error {
	return ws.Set(name, NewUndefined())
}

func (ws *Worksheet) MustIsSet(name string) bool {
	isSet, err := ws.IsSet(name)
	if err != nil {
		panic(err)
	}
	return isSet
}

func (ws *Worksheet) IsSet(name string) (bool, error) {
	// lookup field by name
	field, ok := ws.def.fieldsByName[name]
	if !ok {
		return false, fmt.Errorf("unknown field %s", name)
	}
	index := field.index

	// check presence of value
	_, isSet := ws.data[index]

	return isSet, nil
}

func (ws *Worksheet) MustGet(name string) Value {
	value, err := ws.Get(name)
	if err != nil {
		panic(err)
	}
	return value
}

// TODO(pascal): need to think about proper return type here, should be consistent with Set
func (ws *Worksheet) Get(name string) (Value, error) {
	// lookup field by name
	field, ok := ws.def.fieldsByName[name]
	if !ok {
		return nil, fmt.Errorf("unknown field %s", name)
	}
	index := field.index

	// is a value set for this field?
	value, ok := ws.data[index]
	if !ok {
		return &tUndefined{}, nil
	}

	// type check
	if ok := value.Type().AssignableTo(field.typ); !ok {
		return nil, fmt.Errorf("cannot assign %s to %s", value, field.typ)
	}

	return value, nil
}

func (ws *Worksheet) diff() map[int]Value {
	allIndexes := make(map[int]bool)
	for index := range ws.orig {
		allIndexes[index] = true
	}
	for index := range ws.data {
		allIndexes[index] = true
	}

	diff := make(map[int]Value)
	for index := range allIndexes {
		orig, hasOrig := ws.orig[index]
		data, hasData := ws.data[index]
		if hasOrig && !hasData {
			diff[index] = &tUndefined{}
		} else if !hasOrig && hasData {
			diff[index] = data
		} else if !orig.Equal(data) {
			diff[index] = data
		}
	}

	return diff
}
