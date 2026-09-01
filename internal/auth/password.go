package auth

import "golang.org/x/crypto/bcrypt"

const bcryptCost = 12

func HashPassword(password string) (string, error) {
	value, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(value), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
