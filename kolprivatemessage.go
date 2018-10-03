package main

import (
    "fmt"
    "time"
    "regexp"
    "math/rand"
    "github.com/Hugmeir/kolgo"
)

var firstVerifyRe = regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?!?`)
func HandleKoLDM(kol kolgo.KoLRelay, message kolgo.ChatMessage) (string, error) {
    if !firstVerifyRe.MatchString(message.Msg) {
        return "", nil
    }

    senderId := kol.SenderIdFromMessage(message)

    _, ok := verificationsPending.Load("User:" + message.Who.Name);
    if ok {
        kol.SendMessage("/msg " + senderId, "Already sent you a code, you must wait 5 minutes to generate a new one")
        return "", nil
    }

    verificationCode := fmt.Sprintf("%15d", rand.Uint64())
    verificationsPending.Store("Code:" + verificationCode, message.Who.Name)
    verificationsPending.Store("User:" + message.Who.Name, verificationCode)

    kol.SendMessage("/msg " + senderId, "In Discord, send me a private message saying \"Verify me: " + verificationCode + "\", without the quotes.  This will expire in 5 minutes")

    go func() {
        time.Sleep(5 * time.Minute)
        verificationsPending.Delete("Code:" + verificationCode)
        verificationsPending.Delete("User:" + message.Who.Name)
    }()

    return "", nil
}


