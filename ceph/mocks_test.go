//   Copyright 2024 DigitalOcean
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package ceph

import (
	"encoding/json"

	"github.com/google/go-cmp/cmp"
	mock "github.com/stretchr/testify/mock"
)

// Conn is an autogenerated mock type for the Conn type
type MockConn struct {
	mock.Mock
}

// GetPoolStats provides a mock function with given fields: _a0
func (_m *MockConn) GetPoolStats(_a0 string) (*PoolStat, error) {
	ret := _m.Called(_a0)

	var r0 *PoolStat
	if rf, ok := ret.Get(0).(func(string) *PoolStat); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*PoolStat)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MgrCommand provides a mock function with given fields: _a0
func (_m *MockConn) MgrCommand(_a0 [][]byte) ([]byte, string, error) {
	ret := _m.Called(_a0)

	var r0 []byte
	if rf, ok := ret.Get(0).(func([][]byte) []byte); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]byte)
		}
	}

	var r1 string
	if rf, ok := ret.Get(1).(func([][]byte) string); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Get(1).(string)
	}

	var r2 error
	if rf, ok := ret.Get(2).(func([][]byte) error); ok {
		r2 = rf(_a0)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// MonCommand provides a mock function with given fields: _a0
func (_m *MockConn) MonCommand(_a0 []byte) ([]byte, string, error) {
	ret := _m.Called(_a0)

	var r0 []byte
	if rf, ok := ret.Get(0).(func([]byte) []byte); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]byte)
		}
	}

	var r1 string
	if rf, ok := ret.Get(1).(func([]byte) string); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Get(1).(string)
	}

	var r2 error
	if rf, ok := ret.Get(2).(func([]byte) error); ok {
		r2 = rf(_a0)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

func setupVersionMocks(cephVersion string, cephVersions string) *MockConn {
	conn := &MockConn{}

	conn.On("MonCommand", mock.MatchedBy(func(in interface{}) bool {
		v := map[string]interface{}{}

		_ = json.Unmarshal(in.([]byte), &v)

		return cmp.Equal(v, map[string]interface{}{
			"prefix": "version",
			"format": "json",
		})
	})).Return([]byte(cephVersion), "", nil)

	// versions is only used to check if rbd mirror is present
	conn.On("MonCommand", mock.MatchedBy(func(in interface{}) bool {
		v := map[string]interface{}{}

		_ = json.Unmarshal(in.([]byte), &v)

		return cmp.Equal(v, map[string]interface{}{
			"prefix": "versions",
			"format": "json",
		})
	})).Return([]byte(cephVersions), "", nil)

	return conn
}
