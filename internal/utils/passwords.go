package utils

import (
	"crypto/rand"
	"math/big"
)

func GeneratePassword(length int, includeNumeric bool, includeSpecial bool) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const numeric = "0123456789"
	const special = "!@#$%^&*()_+=-"

	password := make([]byte, length)
	var charSource string

	if includeNumeric {
		charSource += numeric
	}
	if includeSpecial {
		charSource += special
	}
	charSource += charset

	bigLength := big.NewInt(int64(len(charSource)))

	for i := 0; i < length; i++ {
		randNum, err := rand.Int(rand.Reader, bigLength)
		if err != nil {
			panic(err)
		}
		password = append(password, charSource[randNum.Int64()])
	}
	return string(password)
}
