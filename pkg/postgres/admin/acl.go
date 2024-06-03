package admin

import (
	"fmt"
	"regexp"
	"strings"
)

type PermissionACL struct {
	As     string
	For    string
	Read   bool
	Write  bool
	Update bool
	Delete bool
}

// e.g. {user=r/owner}
// oid  | defaclrole | defaclnamespace | defaclobjtype |         defaclacl
// -------+------------+-----------------+---------------+---------------------------
//  16425 |      16384 |            2200 | r             | {other_user=arwd/my_user}
//  16426 |      16384 |            2200 | S             | {other_user=rw/my_user}

var ACL_REGEX = regexp.MustCompile(`^{\"?([\w-_]+)\"?=([ardw]+)\/\"?([\w-_]+)\"?}$`)

func parseACL(acl string) (PermissionACL, error) {
	matches := ACL_REGEX.FindStringSubmatch(acl)
	if len(matches) != 4 {
		return PermissionACL{}, fmt.Errorf("invalid ACL: '%s'", acl)
	}

	return PermissionACL{
		As:     matches[3],
		For:    matches[1],
		Read:   strings.Contains(matches[2], "r"),
		Write:  strings.Contains(matches[2], "w"),
		Update: strings.Contains(matches[2], "a"),
		Delete: strings.Contains(matches[2], "d"),
	}, nil
}
