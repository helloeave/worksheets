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

	uuid "github.com/satori/go.uuid"
)

// Definitions groups all definitions for a workbook, which may consists of
// multiple worksheet definitions, custom types, etc.
type Definitions struct {
	defs map[string]NamedType
}

// parentsRefs records and organizes references to all parents of a worksheet,
// i.e. all worksheets which point directly (ref), or indirectly (e.g. via a
// ref in a slice) to the worksheet.
//
// The map goes from parent's worksheet name, field index of the parent
// pointing to this worksheet, and then the actual references to the parent
// worksheet by ID.
type parentsRefs map[string]map[int]map[string]*Worksheet

func (parents parentsRefs) addParentViaFieldIndex(parent *Worksheet, fieldIndex int) {
	byParentFieldIndex, ok := parents[parent.def.name]
	if !ok {
		byParentFieldIndex = make(map[int]map[string]*Worksheet)
		parents[parent.def.name] = byParentFieldIndex
	}
	byParentId, ok := byParentFieldIndex[fieldIndex]
	if !ok {
		byParentId = make(map[string]*Worksheet)
		byParentFieldIndex[fieldIndex] = byParentId
	}
	byParentId[parent.Id()] = parent
}

func (parents parentsRefs) removeParentViaFieldIndex(parent *Worksheet, fieldIndex int) {
	parentName := parent.def.name

	if _, ok := parents[parentName]; !ok {
		return
	} else if _, ok := parents[parentName][fieldIndex]; !ok {
		return
	}

	delete(parents[parentName][fieldIndex], parent.Id())
	if len(parents[parentName][fieldIndex]) == 0 {
		delete(parents[parentName], fieldIndex)
	}
	if len(parents[parentName]) == 0 {
		delete(parents, parentName)
	}
}

// Worksheet is ... TODO(pascal): documentation binge
type Worksheet struct {
	// def holds the definition of this worksheet.
	def *Definition

	// orig holds the worksheet data as it was when it was initially loaded.
	orig map[int]Value

	// data holds all the worksheet data.
	data map[int]Value

	// parents holds all the reverse pointers of worksheets pointing to this
	// worksheet.
	parents parentsRefs
}

const (
	// indexId is the reserved index to store a worksheet's identifier.
	indexId = -2

	// indexVersion is the reserved index to store a worksheet's version.
	indexVersion = -1
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

func MustNewDefinitions(reader io.Reader, opts ...Options) *Definitions {
	defs, err := NewDefinitions(reader, opts...)
	if err != nil {
		panic(err)
	}
	return defs
}

// NewDefinitions parses one or more worksheet definitions, and creates worksheet
// models from them.
func NewDefinitions(reader io.Reader, opts ...Options) (*Definitions, error) {
	p := newParser(reader)
	allDefs, err := p.parseDefinitions()
	if err != nil {
		return nil, err
	}

	defs := make(map[string]NamedType)
	for _, def := range allDefs {
		name := def.Name()
		if _, exists := defs[name]; exists {
			return nil, fmt.Errorf("multiple types %s", name)
		}
		defs[name] = def
	}

	err = processOptions(defs, opts...)
	if err != nil {
		return nil, err
	}

	for _, typ := range defs {
		def, ok := typ.(*Definition)
		if !ok {
			continue
		}
		for _, field := range def.fieldsByIndex {
			// Any unresolved externals?
			if _, ok := field.computedBy.(*tExternal); ok {
				return nil, fmt.Errorf("%s.%s: missing plugin for external computed_by", def.name, field.name)
			}

			// Any unknown refs types?
			if err := resolveRefTypes(fmt.Sprintf("%s.%s", def.name, field.name), defs, field); err != nil {
				return nil, err
			}
		}
	}

	// Resolve computed_by & constrained_by dependencies
	for _, typ := range defs {
		def, ok := typ.(*Definition)
		if !ok {
			continue
		}
		for _, field := range def.fieldsByIndex {
			fieldTrigger := field.computedBy
			if fieldTrigger == nil {
				fieldTrigger = field.constrainedBy
			}

			if fieldTrigger != nil {
				selectors := fieldTrigger.selectors()
				if len(selectors) == 0 {
					return nil, fmt.Errorf("%s.%s has no dependencies", def.name, field.name)
				}
				for _, selector := range selectors {
					path, ok := selector.Select(def)
					if !ok {
						return nil, fmt.Errorf("%s.%s references unknown arg %s", def.name, field.name, selector)
					}

					// Only update the graph for computed fields; constrained
					// fields don't need to be recalculated when args are
					// set, only upon setting a new value.
					if field.computedBy != nil {
						for _, ascendant := range path {
							ascendant.dependents = append(ascendant.dependents, field)
						}
					}
				}
			}
		}
	}

	return &Definitions{
		defs,
	}, nil
}

func (s tSelector) Select(elemType Type) ([]*Field, bool) {
	switch typ := elemType.(type) {
	case *Definition:
		field, ok := typ.fieldsByName[s[0]]
		if !ok {
			return nil, false
		}
		var subPath []*Field
		if len(s) > 1 {
			var ok bool
			subPath, ok = tSelector(s[1:]).Select(field.typ)
			if !ok {
				return nil, false
			}
		}
		subPath = append(subPath, field)
		return subPath, true
	case *SliceType:
		return s.Select(typ.elementType)
	}

	return nil, false
}

// resolveRefTypes resolves type references, e.g. `some_name`, to the actual
// type definition for these references. During parsing, empty instances of
// `Definition` are used, which are here replaced with the actual proper
// definition from the `defs` map.
func resolveRefTypes(niceFieldName string, defs map[string]NamedType, locus interface{}) error {
	switch locus.(type) {
	case *Field:
		field := locus.(*Field)
		if refTyp, ok := field.typ.(*Definition); ok {
			refDef, ok := defs[refTyp.name]
			if !ok {
				return fmt.Errorf("%s: unknown type %s", niceFieldName, refTyp.name)
			}
			field.typ = refDef
		}
		if _, ok := field.typ.(*SliceType); ok {
			return resolveRefTypes(niceFieldName, defs, field.typ)
		}
	case *SliceType:
		sliceType := locus.(*SliceType)
		if refTyp, ok := sliceType.elementType.(*Definition); ok {
			refDef, ok := defs[refTyp.name]
			if !ok {
				return fmt.Errorf("%s: unknown type %s", niceFieldName, refTyp.name)
			}
			sliceType.elementType = refDef
		}
		return resolveRefTypes(niceFieldName, defs, sliceType.elementType)
	}

	return nil
}

func processOptions(defs map[string]NamedType, opts ...Options) error {
	if len(opts) == 0 {
		return nil
	} else if len(opts) != 1 {
		return fmt.Errorf("too many options provided")
	}

	opt := opts[0]

	for name, plugins := range opt.Plugins {
		// When we add constrained types, we'd want to be able to use plugins
		// to define their constraints, and will need to generalize this
		// code.
		typ, ok := defs[name]
		if !ok {
			return fmt.Errorf("plugins: unknown worksheet %s", name)
		}
		def, ok := typ.(*Definition)
		if !ok {
			return fmt.Errorf("plugins: unknown worksheet %s", name)
		}
		err := attachPluginsToFields(def, plugins)
		if err != nil {
			return err
		}
	}
	return nil
}

func attachPluginsToFields(def *Definition, plugins map[string]ComputedBy) error {
	for fieldName, plugin := range plugins {
		field, ok := def.fieldsByName[fieldName]
		if !ok {
			return fmt.Errorf("plugins: unknown field %s.%s", def.name, fieldName)
		}
		if _, ok := field.computedBy.(*tExternal); !ok {
			if _, ok := field.constrainedBy.(*tExternal); !ok {
				return fmt.Errorf("plugins: field %s.%s not externally defined", def.name, fieldName)
			} else {
				field.constrainedBy = &ePlugin{plugin}
			}
		} else {
			field.computedBy = &ePlugin{plugin}
		}
	}
	return nil
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
	id := uuid.Must(uuid.NewV4())

	if err := ws.Set("id", NewText(id.String())); err != nil {
		panic(fmt.Sprintf("unexpected %s", err))
	}

	// version
	if err := ws.Set("version", NewNumberFromInt(1)); err != nil {
		panic(fmt.Sprintf("unexpected %s", err))
	}

	// computedBy
	for _, field := range ws.def.fieldsByIndex {
		if field.computedBy != nil {
			value, err := field.computedBy.compute(ws)
			if err != nil {
				return nil, err
			}
			ws.set(field, value)
		}
	}

	return ws, nil
}

func (defs *Definitions) newUninitializedWorksheet(name string) (*Worksheet, error) {
	typ, ok := defs.defs[name]
	if !ok {
		return nil, fmt.Errorf("unknown worksheet %s", name)
	}
	def, ok := typ.(*Definition)
	if !ok {
		return nil, fmt.Errorf("unknown worksheet %s", name)
	}

	return def.newUninitializedWorksheet(), nil
}

func (def *Definition) newUninitializedWorksheet() *Worksheet {
	return &Worksheet{
		def:     def,
		orig:    make(map[int]Value),
		data:    make(map[int]Value),
		parents: make(map[string]map[int]map[string]*Worksheet),
	}
}

func (ws *Worksheet) Id() string {
	return ws.data[indexId].(*Text).value
}

func (ws *Worksheet) Version() int {
	return int(ws.data[indexVersion].(*Number).value)
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

	if field.computedBy != nil {
		return fmt.Errorf("cannot assign to computed field %s", name)
	}

	if _, ok := field.typ.(*SliceType); ok {
		return fmt.Errorf("Set on slice field %s, use Append, or Del", name)
	}

	if field.constrainedBy != nil {
		prevValue := ws.MustGet(name)

		// plan rollback
		hasFailed := true
		defer func() {
			if hasFailed {
				ws.set(field, prevValue)
			}
		}()

		err := ws.set(field, value)
		if err != nil {
			return err
		}
		constrainedByResult, err := field.constrainedBy.compute(ws)
		if err != nil {
			return err
		}
		if val, ok := constrainedByResult.(*Bool); ok && val.value {
			hasFailed = false
			return nil
		} else {
			return fmt.Errorf("%s not a valid value for constrained field %s", value.String(), name)
		}
	}

	err := ws.set(field, value)
	return err
}

func (ws *Worksheet) set(field *Field, value Value) error {
	var (
		index          = field.index
		_, isUndefined = value.(*Undefined)
	)

	// oldValue
	oldValue, ok := ws.data[index]
	if !ok {
		oldValue = vUndefined
	}

	// ident
	if oldValue.Equal(value) {
		return nil
	}

	// assignability check
	if err := canAssignTo("assign", value, field.typ); err != nil {
		return err
	}

	// store
	if isUndefined {
		delete(ws.data, index)
	} else {
		ws.data[index] = value
	}

	// dependents
	if err := ws.handleDependentUpdates(field, oldValue, value); err != nil {
		return err
	}

	return nil
}

func (ws *Worksheet) MustUnset(name string) {
	if err := ws.Unset(name); err != nil {
		panic(err)
	}
}

func (ws *Worksheet) Unset(name string) error {
	if field, ok := ws.def.fieldsByName[name]; ok {
		if _, ok := field.typ.(*SliceType); ok {
			return fmt.Errorf("Unset on slice field names, must use Del")
		}
	}
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

func (ws *Worksheet) MustGetSlice(name string) []Value {
	slice, err := ws.GetSlice(name)
	if err != nil {
		panic(err)
	}
	return slice
}

func (ws *Worksheet) GetSlice(name string) ([]Value, error) {
	_, slice, err := ws.getSlice(name)
	if err != nil {
		return nil, err
	} else if slice == nil {
		return nil, nil
	}

	return slice.Elements(), nil
}

func (ws *Worksheet) getSlice(name string) (*Field, *Slice, error) {
	field, value, err := ws.get(name)
	if err != nil {
		return nil, nil, err
	}

	_, ok := field.typ.(*SliceType)
	if !ok {
		return field, nil, fmt.Errorf("GetSlice on non-slice field %s, use Get", name)
	}

	return field, value.(*Slice), nil
}

// Get gets a value for base types, e.g. text, number, or bool.
// For other kinds of values, use specific getters such as `GetSlice`.
func (ws *Worksheet) Get(name string) (Value, error) {
	field, value, err := ws.get(name)
	if err != nil {
		return nil, err
	}

	if _, ok := field.typ.(*SliceType); ok {
		return nil, fmt.Errorf("Get on slice field %s, use GetSlice", name)
	}

	return value, err
}

func (ws *Worksheet) get(name string) (*Field, Value, error) {
	// lookup field by name
	field, ok := ws.def.fieldsByName[name]
	if !ok {
		return nil, nil, fmt.Errorf("unknown field %s", name)
	}
	index := field.index

	// is a value set for this field?
	value, ok := ws.data[index]
	if !ok {
		if sliceType, ok := field.typ.(*SliceType); ok {
			return field, newSlice(sliceType), nil
		} else {
			return field, vUndefined, nil
		}
	}

	return field, value, nil
}

func (ws *Worksheet) MustAppend(name string, value Value) {
	if err := ws.Append(name, value); err != nil {
		panic(err)
	}
}

func (ws *Worksheet) Append(name string, element Value) error {
	// lookup field by name
	field, ok := ws.def.fieldsByName[name]
	if !ok {
		return fmt.Errorf("unknown field %s", name)
	}
	index := field.index

	sliceType, ok := field.typ.(*SliceType)
	if !ok {
		return fmt.Errorf("Append on non-slice field %s", name)
	}

	// is a value set for this field?
	value, ok := ws.data[index]
	if !ok {
		value = newSlice(sliceType)
		ws.data[index] = value
	}

	// append
	slice := value.(*Slice)
	slice, err := slice.doAppend(element)
	if err != nil {
		return err
	}
	ws.data[index] = slice

	// dependents
	if err := ws.handleDependentUpdates(field, nil, element); err != nil {
		return err
	}

	return nil
}

func (ws *Worksheet) MustDel(name string, index int) {
	if err := ws.Del(name, index); err != nil {
		panic(err)
	}
}

func (ws *Worksheet) Del(name string, index int) error {
	field, slice, err := ws.getSlice(name)
	if err != nil {
		if field != nil {
			if _, ok := field.typ.(*SliceType); !ok {
				return fmt.Errorf("Del on non-slice field %s", name)
			}
		}
		return err
	}

	newSlice, err := slice.doDel(index)
	if err != nil {
		return err
	}
	deletedValue := slice.elements[index].value
	ws.data[field.index] = newSlice

	// dependents
	if err := ws.handleDependentUpdates(field, deletedValue, nil); err != nil {
		return err
	}

	return nil
}

func (ws *Worksheet) handleDependentUpdates(field *Field, oldValue, newValue Value) error {
	for _, dependentField := range field.dependents {
		// 1. Gather all dependent worksheets which point to this worksheet,
		// and need to be triggered.
		var allDependents []*Worksheet
		if dependentField.def == ws.def {
			allDependents = []*Worksheet{ws}
		} else {
			for _, parentsByFieldIndex := range ws.parents[dependentField.def.name] {
				for _, parent := range parentsByFieldIndex {
					allDependents = append(allDependents, parent)
				}
			}
		}

		// 2. Trigger the compute by of all dependent worksheets.
		for _, dependent := range allDependents {
			updatedValue, err := dependentField.computedBy.compute(dependent)
			if err != nil {
				return err
			}
			if err := dependent.set(dependentField, updatedValue); err != nil {
				return err
			}
		}
	}

	// Add ws to parent pointers of newValue.
	for _, childWs := range extractChildWs(newValue) {
		childWs.parents.addParentViaFieldIndex(ws, field.index)
	}

	// Remove ws from parent pointers of oldValue.
	for _, childWs := range extractChildWs(oldValue) {
		childWs.parents.removeParentViaFieldIndex(ws, field.index)
	}

	return nil
}

func canAssignTo(op string, value Value, typ Type) error {
	valueTyp := value.Type()
	if !value.assignableTo(typ) {
		var (
			valueStr                 string
			valueAsText, valueIsText = value.(*Text)
			_, typIsEnum             = typ.(*EnumType)
		)
		if valueIsText && typIsEnum {
			// We allow the value to leak into the error message in the special
			// case of assigning a text to an enum.
			valueStr = valueAsText.value
		} else {
			valueStr = fmt.Sprintf("value of type %s", valueTyp)
		}
		switch op {
		case "assign":
			return fmt.Errorf("cannot %s %s to %s", op, valueStr, typ)
		case "append":
			return fmt.Errorf("cannot %s %s to []%s", op, valueStr, typ)
		default:
			panic("unexpected")
		}
	}

	return nil
}

func (value *Undefined) assignableTo(_ Type) bool {
	return true
}

func (value *Text) assignableTo(u Type) bool {
	if _, ok := u.(*TextType); ok {
		return true
	}

	if enumTyp, ok := u.(*EnumType); ok {
		return enumTyp.elements[value.value]
	}

	return false
}

func (value *Bool) assignableTo(u Type) bool {
	_, ok := u.(*BoolType)
	return ok
}

func (value *Number) assignableTo(u Type) bool {
	uNum, ok := u.(*NumberType)
	return ok && value.typ.scale <= uNum.scale
}

func (value *Slice) assignableTo(u Type) bool {
	other, ok := u.(*SliceType)
	if !ok {
		return false
	}

	// See note on Value#assignableTo about dynamic checks, the loop below
	// is the largest runtime cost caused by lack of automatic boxing.
	for _, element := range value.elements {
		if !element.value.assignableTo(other.elementType) {
			return false
		}
	}
	return true
}

func (value *Worksheet) assignableTo(u Type) bool {
	// Since we do type resolution, pointer equality suffices to
	// guarantee assignability.
	return value.def == u
}

func extractChildWs(value Value) []*Worksheet {
	switch v := value.(type) {
	case *Worksheet:
		return []*Worksheet{v}
	case *wsRefAtVersion:
		return []*Worksheet{v.ws}
	case *Slice:
		var result []*Worksheet
		for _, element := range v.elements {
			result = append(result, extractChildWs(element.value)...)
		}
		return result
	default:
		return nil
	}
}
