package main

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "os"

    "golang.org/x/net/context"
    "golang.org/x/oauth2"
    "golang.org/x/oauth2/google"
    "google.golang.org/api/sheets/v4"
)

func NewBlackListPoller(credentialsFile, tokenFile string) *sheets.Service {
    b, err := ioutil.ReadFile(credentialsFile)
    if err != nil {
        fmt.Println("Could not read credentials: ", err)
        return nil
    }

    config, err := google.ConfigFromJSON(b, "https://www.googleapis.com/auth/spreadsheets.readonly")
    if err != nil {
        fmt.Println("Unable to parse client secret file to config:", err)
        return nil
    }

    tok, err := tokenFromFile(tokenFile)
    if err != nil {
        fmt.Println("Failed to parse token: ", err)
        return nil
    }

    client := config.Client(context.Background(), tok)
    srv, err := sheets.New(client)
    if err != nil {
        fmt.Println("Error creating sheets client: ", err)
        return nil
    }

    return srv
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
    f, err := os.Open(file)
    if err != nil {
            return nil, err
    }
    defer f.Close()
    tok := &oauth2.Token{}
    err = json.NewDecoder(f).Decode(tok)
    return tok, err
}

const (
    BLName int = iota
    BLID
    BLWarned
    BLBlacklisted
    BLDisabled
    BLReason
)

type ClanBlacklistEntry struct {
    Name   string
    ID     string
    Reason string
}

type GoogleSheetsConfig struct {
    CredentialsFile string
    TokenFile       string

    SpreadsheetId   string
    ReadRange       string
}

func ReadBlacklist(conf *GoogleSheetsConfig) []ClanBlacklistEntry {
    srv := NewBlackListPoller(conf.CredentialsFile, conf.TokenFile)
    blacklist := []ClanBlacklistEntry{}
    if srv == nil {
        return blacklist
    }

    resp, err := srv.Spreadsheets.Values.Get(conf.SpreadsheetId, conf.ReadRange).Do()
    if err != nil {
        fmt.Println("Could not load blacklist: ", err)
        return blacklist
    }

    if len(resp.Values) == 0 {
        return blacklist
    }

    for _, row := range resp.Values {
        if len(row) < 4 {
            continue
        }
        if blacklisted, ok := row[BLBlacklisted].(string); !ok || blacklisted == "" {
            continue
        }
        name, _   := row[BLName].(string)
        id, _     := row[BLID].(string)
        var reason string
        if len(row) < 6 {
            reason = ""
        } else {
            reason, _ = row[BLReason].(string)
        }
        if name == "" && id == "" {
            // Weird broken row?
            continue
        }
        blacklist = append(blacklist, ClanBlacklistEntry{ name, id, reason })
    }
    return blacklist
}

