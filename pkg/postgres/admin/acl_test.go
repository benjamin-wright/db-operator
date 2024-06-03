package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseACL(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output PermissionACL
		err    string
	}{
		{
			name:  "empty",
			input: "",
			err:   "invalid ACL: ''",
		},
		{
			name:  "invalid",
			input: "invalid",
			err:   "invalid ACL: 'invalid'",
		},
		{
			name:  "reader",
			input: "{user=r/owner}",
			output: PermissionACL{
				For:  "user",
				As:   "owner",
				Read: true,
			},
		},
		{
			name:  "writer",
			input: "{user=rw/owner}",
			output: PermissionACL{
				For:   "user",
				As:    "owner",
				Read:  true,
				Write: true,
			},
		},
		{
			name:  "allwrite",
			input: "{user=arwd/owner}",
			output: PermissionACL{
				For:    "user",
				As:     "owner",
				Read:   true,
				Write:  true,
				Update: true,
				Delete: true,
			},
		},
		{
			name:  "users with specical characters",
			input: "{\"user-rand\"=r/\"owner-user\"}",
			output: PermissionACL{
				For:  "user-rand",
				As:   "owner-user",
				Read: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := parseACL(test.input)
			if test.err != "" {
				assert.EqualError(t, err, test.err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, test.output, output)
		})
	}
}
