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
	"math"

	"github.com/stretchr/testify/require"
	"gopkg.in/mgutz/dat.v2/sqlx-runner"
)

func (s *DbZuite) TestExample() {
	ws := s.store.defs.MustNewWorksheet("simple")
	ws.MustSet("name", NewText("Alice"))

	s.MustRunTransaction(func(tx *runner.Tx) error {
		session := s.store.Open(tx)
		return session.Save(ws)
	})

	var wsFromStore *Worksheet
	s.MustRunTransaction(func(tx *runner.Tx) error {
		session := s.store.Open(tx)
		var err error
		wsFromStore, err = session.Load("simple", ws.Id())
		return err
	})

	require.Equal(s.T(), `"Alice"`, wsFromStore.MustGet("name").String())
}

func (s *DbZuite) TestSave() {
	ws, err := s.store.defs.NewWorksheet("simple")
	require.NoError(s.T(), err)

	err = ws.Set("name", NewText("Alice"))
	require.NoError(s.T(), err)

	s.MustRunTransaction(func(tx *runner.Tx) error {
		session := s.store.Open(tx)
		return session.Save(ws)
	})

	wsRecs, valuesRecs := s.DbState()

	require.Equal(s.T(), []rWorksheet{
		{
			Id:      ws.Id(),
			Version: 1,
			Name:    "simple",
		},
	}, wsRecs)

	require.Equal(s.T(), []rValue{
		{
			Id:          IdAt(valuesRecs, 0),
			WorksheetId: ws.Id(),
			Index:       IndexId,
			FromVersion: 1,
			ToVersion:   math.MaxInt32,
			Value:       fmt.Sprintf(`"%s"`, ws.Id()),
		},
		{
			Id:          IdAt(valuesRecs, 1),
			WorksheetId: ws.Id(),
			Index:       IndexVersion,
			FromVersion: 1,
			ToVersion:   math.MaxInt32,
			Value:       `1`,
		},
		{
			Id:          IdAt(valuesRecs, 2),
			WorksheetId: ws.Id(),
			Index:       83,
			FromVersion: 1,
			ToVersion:   math.MaxInt32,
			Value:       `"Alice"`,
		},
	}, valuesRecs)

	// Upon Save, orig needs to be set to data.
	require.Empty(s.T(), ws.diff())
}

func (s *DbZuite) TestUpdate() {
	ws, err := s.store.defs.NewWorksheet("simple")
	require.NoError(s.T(), err)

	err = ws.Set("name", NewText("Alice"))
	require.NoError(s.T(), err)

	s.MustRunTransaction(func(tx *runner.Tx) error {
		session := s.store.Open(tx)
		return session.Save(ws)
	})

	err = ws.Set("name", NewText("Bob"))
	require.NoError(s.T(), err)

	s.MustRunTransaction(func(tx *runner.Tx) error {
		session := s.store.Open(tx)
		return session.Update(ws)
	})

	wsRecs, valuesRecs := s.DbState()

	require.Equal(s.T(), []rWorksheet{
		{
			Id:      ws.Id(),
			Version: 2,
			Name:    "simple",
		},
	}, wsRecs)

	require.Equal(s.T(), []rValue{
		{
			Id:          IdAt(valuesRecs, 0),
			WorksheetId: ws.Id(),
			Index:       IndexId,
			FromVersion: 1,
			ToVersion:   math.MaxInt32,
			Value:       fmt.Sprintf(`"%s"`, ws.Id()),
		},
		{
			Id:          IdAt(valuesRecs, 1),
			WorksheetId: ws.Id(),
			Index:       IndexVersion,
			FromVersion: 1,
			ToVersion:   1,
			Value:       `1`,
		},
		{
			Id:          IdAt(valuesRecs, 2),
			WorksheetId: ws.Id(),
			Index:       IndexVersion,
			FromVersion: 2,
			ToVersion:   math.MaxInt32,
			Value:       `2`,
		},
		{
			Id:          IdAt(valuesRecs, 3),
			WorksheetId: ws.Id(),
			Index:       83,
			FromVersion: 1,
			ToVersion:   1,
			Value:       `"Alice"`,
		},
		{
			Id:          IdAt(valuesRecs, 4),
			WorksheetId: ws.Id(),
			Index:       83,
			FromVersion: 2,
			ToVersion:   math.MaxInt32,
			Value:       `"Bob"`,
		},
	}, valuesRecs)

	// Upon update, version must increase
	require.Equal(s.T(), 2, ws.Version())

	// Upon Update, orig needs to be set to data.
	require.Empty(s.T(), ws.diff())
}

func IdAt(s []rValue, index int) int64 {
	if 0 <= index && index < len(s) {
		return s[index].Id
	}
	return 0
}

func (s *DbZuite) MustRunTransaction(fn func(tx *runner.Tx) error) {
	err := RunTransaction(s.db, fn)
	require.NoError(s.T(), err)
}

func (s *DbZuite) DbState() ([]rWorksheet, []rValue) {
	var (
		wsRecs     []rWorksheet
		valuesRecs []rValue
	)

	if err := s.db.
		Select("*").
		From("worksheets").
		OrderBy("id").
		QueryStructs(&wsRecs); err != nil {
		require.NoError(s.T(), err)
	}

	if err := s.db.
		Select("*").
		From("worksheet_values").
		OrderBy("worksheet_id, index, from_version").
		QueryStructs(&valuesRecs); err != nil {
		require.NoError(s.T(), err)
	}

	return wsRecs, valuesRecs
}