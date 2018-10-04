package main

import (
    "fmt"
    "regexp"
    "os"
    "encoding/json"
    "os/exec"
    "errors"
    "bytes"
    "strings"
    "strconv"
    "github.com/bwmarrin/discordgo"
)

type dmHandlers struct {
    re *regexp.Regexp
    cb func(*Chatbot, *discordgo.Session, *discordgo.MessageCreate, []string)
}

func HandleVerification(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
    verificationCode := matches[1]
    result, ok := bot.VerificationPending.Load("Code:" + verificationCode)
    if ok {
        // Insert in the db:
        go InsertNewNickname(m.Author.ID, result.(string))
        // Put in our in-memory hash:
        bot.NameOverride.Store(m.Author.ID, result.(string))
        // Let 'em know:
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("That's you alright!  I'll call you %s from now on", result.(string)))
    } else {
        // Hmm...
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Incorrect verification code: '%s'", verificationCode))
    }
}

var commandsThatReturnHTML = map[string]bool{
    "/who":   true,
    "/whois": true,
}
func HandleCommandForGame(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
    if !bot.SenderCanRunCommands(s, m) {
        return
    }

    cmd  := matches[1]
    args := matches[2]

    body, err := bot.KoL.SubmitChat(cmd, args)
    if err != nil {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command run FAILED, error: ```css\n%s\n```", err))
        return
    }

    var gameOutput string
    if _, shouldFormat := commandsThatReturnHTML[cmd]; shouldFormat {
        var cmdJson map[string]interface{}
        err = json.Unmarshal(body, &cmdJson)
        if err == nil {
            if output, ok := cmdJson[`output`]; ok {
                gameOutput = EscapeDiscordMetaCharacters(FormatGameOutput([]byte(output.(string))))
            }
        } else {
            fmt.Println("error decoding comamnd output: ", err)
        }
    }

    if gameOutput == "" {
        gameOutput = EscapeDiscordMetaCharacters(string(body))
    }

    s.ChannelMessageSend(m.ChannelID, "Command run, output: ```css\n" + gameOutput + "\n```")
}

var tryLynx = false
func DetectLynx() bool {
    str, err := exec.LookPath("lynx")
    if err != nil {
        return false
    }

    if str == "" {
        return false
    }

    cmd := exec.Command("lynx", "-help")
    var out bytes.Buffer
    cmd.Stdout = &out
    err  = cmd.Run()
    if err != nil {
        return false
    }

    if err != nil {
        return false
    }

    if !strings.Contains(out.String(), `-dump`) {
        return false
    }

    // What a gauntlet!
    return true
}

// Try formatting the game output with lynx
func FormatGameOutput(o []byte) string {
    if !tryLynx {
        return EscapeDiscordMetaCharacters(string(o))
    }

    cmd := exec.Command("lynx", "--dump", "--stdin")
    cmd.Stdin = bytes.NewReader(o)
    var out bytes.Buffer
    cmd.Stdout = &out
    err := cmd.Run()
    if err != nil {
        return EscapeDiscordMetaCharacters(string(o))
    }

    str := out.String()
    // Cut off the 'References' which will point to nothing useful
    idx := strings.Index(str, "\nReferences\n\n")
    if idx > 0 {
        str = str[:idx]
    }
    return EscapeDiscordMetaCharacters(str)
}

// TODO: Move this into kolgo and just steal the item list from mafia
type ItemType int
const (
    Spleen ItemType = iota
    Usable
)
type Item struct {
    ID   string
    Name string
    Type ItemType
}
var itemNameToID = map[string]*Item{
    "sleaze wad": &Item{
        ID:   "1455",
        Name: "sleaze wad",
        Type: Spleen,
    },
    "mojo filter": &Item{
        ID: "2614",
        Name: "mojo filter",
        Type: Usable,
    },
}
var validItemID = regexp.MustCompile(`\A[0-9]+\z`)
func ValidItemID(itemID string) bool {
    return validItemID.MatchString(itemID)
}
func HandleUseCommand(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
    if !bot.SenderCanRunCommands(s, m) {
        return
    }

    itemID         := matches[2]
    quantityStr    := matches[1]
    actualItem, ok := itemNameToID[strings.ToLower(itemID)]
    if ok {
        itemID = actualItem.ID
    }

    if !ValidItemID(itemID) {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Sorry, don't know what to do with that item"))
        return
    }

    quantity := 1
    q, err := strconv.Atoi(quantityStr)
    if err == nil && q > 0 {
        quantity = q
    }

    output, err  := bot.KoL.InvUse(itemID, quantity)
    if err != nil {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command run FAILED, error: ```css\n%s\n```", err))
        return
    }

    formattedOutput := FormatGameOutput(output)

    s.ChannelMessageSend(m.ChannelID, "Command run, output: ```css\n" + formattedOutput + "\n```")
}

func HandleChewCommand(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
    if !bot.SenderCanRunCommands(s, m) {
        return
    }

    itemID         := matches[1]
    actualItem, ok := itemNameToID[strings.ToLower(itemID)]
    if ok {
        itemID = actualItem.ID
    }

    if !ValidItemID(itemID) {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Sorry, don't know what to do with that item"))
        return
    }

    output, err  := bot.KoL.InvSpleen(itemID)
    if err != nil {
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command run FAILED, error: ```css\n%s\n```", err))
        return
    }

    formattedOutput := FormatGameOutput(output)

    s.ChannelMessageSend(m.ChannelID, "Command run, output: ```css\n" + formattedOutput + "\n```")
}

var allDMHandlers = []dmHandlers {
    dmHandlers {
        regexp.MustCompile(`(?i)^\s*verify(?:\s* me)?: ([0-9]{10,})`),
        HandleVerification,
    },
    dmHandlers {
        // !cmd /...
        // Will execute the /... in-game as the relay:
        //  !cmd /msg Hugmeir oh hey boss
        // That will send me a message.  Don't spam me >.>
        regexp.MustCompile(`(?i)!(?:cmd|powerword) (/[^\s]+)(\s*.*)`),
        HandleCommandForGame,
    },
    dmHandlers {
        // !cmd alias ...
        //
        // Does nothing.  !cmd alias only works on the main channel, to prevent
        // stealthy names.
        regexp.MustCompile(`(?i)!(?:cmd|powerword) alias`),
        func (bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            s.ChannelMessageSend(m.ChannelID, "To prevent shenanigans, the alias command only works on the main channel, NOT through direcct message")
        },
    },
    dmHandlers {
        // !cmd Kill
        //
        // This is the killswitch for the relay.
        //
        // It stops the relay and prevents it from coming back up until manually checked by someone
        // with access to the box it is running on.
        //
        // For use in emergencies!
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) Kill\z`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            if !bot.SenderCanRunCommands(s, m) {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
                return
            }

            _, err := os.OpenFile(bot.KillFile, os.O_RDONLY|os.O_CREATE, 0666)
            if err != nil {
                s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Going down, but could not prevent respawning, so the bot will return in 5 minutes.  Reason given: %s", err))
            } else {
                s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Going down, will NOT come back until the killfile is manually removed"))
            }
            panic(errors.New(fmt.Sprintf("*Killed* by %s", m.Author.Username)))
        },
    },
    dmHandlers {
        // !cmd Crash
        //
        // Will crash the relay.  It will come back in ~5 minutes or so.
        // Basically a 'did you turn it off and on again' command.
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) Crash\z`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            if bot.SenderCanRunCommands(s, m) {
                s.ChannelMessageSend(m.ChannelID, "Crashing, bot should return in ~5m")
                panic(errors.New(fmt.Sprintf("Asked to crash by %s", m.Author.Username)))
            } else {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
            }
        },
    },
    dmHandlers {
        // !cmd stfu
        // !cmd stop
        //
        // Will make it stop relaying messages.
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) (?:Relay(?:Bot),?\s+)?(?:stfu|stop)`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            if bot.SenderCanRunCommands(s, m) {
                bot.GlobalStfu = true
                s.ChannelMessageSend(m.ChannelID, "Floodgates are CLOSED.  No messages will be relayed")
            } else {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
            }
        },
    },
    dmHandlers {
        // !cmd use AMOUNT ITEMID/[ITEM NAME]
        //
        // Will make it use the AMOUNT of ITEM
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword)\s+use\s+(\d)\s+(.+)`),
        HandleUseCommand,
    },
    dmHandlers {
        // !cmd chew ITEMID
        //
        // Will make it chew (spleen-use) the item
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword)\s+chew\s+(.+)`),
        HandleChewCommand,
    },
    dmHandlers {
        // !cmd spam on
        // !cmd start
        //
        // Will make it start relaying messages if previously stfu'd
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) (?:Relay(?:Bot),?\s+)?(?:spam on|start)`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            if bot.SenderCanRunCommands(s, m) {
                bot.GlobalStfu  = false
                bot.PartialStfu = false
                s.ChannelMessageSend(m.ChannelID, "Floodgates are open")
            } else {
                s.ChannelMessageSend(m.ChannelID, "That would've totes done something if you had the rights to do the thing.")
            }
        },
    },
}

func ComesFromDM(s *discordgo.Session, m *discordgo.MessageCreate) (bool, error) {
    channel, err := s.State.Channel(m.ChannelID)
    if err != nil {
        if channel, err = s.Channel(m.ChannelID); err != nil {
            return false, err
        }
    }

    return channel.Type == discordgo.ChannelTypeDM, nil
}

func (bot *Chatbot)HandleDM(s *discordgo.Session, m *discordgo.MessageCreate) {
    for _, handler := range allDMHandlers {
        re      := handler.re
        matches := re.FindStringSubmatch(m.Content)
        if len(matches) > 0 {
            go handler.cb(bot, s, m, matches)
            // One match per DM
            return
        }
    }

    s.ChannelMessageSend(m.ChannelID, "<some funny message about not understanding what you mean>");
}


