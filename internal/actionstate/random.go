package actionstate

import "crypto/rand"

func readCryptoRandom(buffer []byte) (int, error) {
	return rand.Read(buffer)
}
