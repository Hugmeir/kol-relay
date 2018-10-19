package main

import (
    "fmt"
    "regexp"
    "strings"

    "os"
    "time"
    "encoding/json"

    "unicode"
    "unicode/utf8"
    "golang.org/x/text/unicode/norm"
    "golang.org/x/text/unicode/runenames"
    "golang.org/x/text/encoding/charmap"

    "github.com/bwmarrin/discordgo"
)
const maxEmoji = 3
func EmojiNoMore(s string) string {
    seenEmoji := 0
    for i, w := 0, 0; i < len(s); i += w {
        c, width := utf8.DecodeRuneInString(s[i:])
        w = width
        if c > 0xFF && unicode.IsSymbol(c) {
            if seenEmoji++; seenEmoji > maxEmoji {
                return "[Bunch of emojis]"
            }
            name := runenames.Name(c)
            s = s[:i] + "[" + name + "]" + s[i+w:]
            w = len(name) + 2
        }
    }
    return s
}

// This should be [\p{Latin1}\p{ASCII}], but no such thing in golang
var nonLatin1Re = regexp.MustCompile(`[^\x00-\xff]`)
func ClearNonLatin1Characters(s string) string {
    // KoL chat only accepts the latin1 range:
    return nonLatin1Re.ReplaceAllString(s, ``)
}

func sanitizeForKoL (content string) string {

    // Remove smallcaps and similar deviants
    content = AsciiFold(content)

    // Try to convert emoji into their character names.  Makes for
    // weirder messages but...
    content = EmojiNoMore(content)

    // Try to normalize into NFC, so that combining characters
    // in the latin1 range look reasonable
    content = norm.NFC.String(content)

    content = ClearNonLatin1Characters(content)

    // KoL chat only accepts latin1, so encode before sending:
    encoded, err := charmap.ISO8859_1.NewEncoder().String(content)
    if err != nil {
        fmt.Printf("Failed to encode message: %v\n", err)
        encoded = content
    }

    return encoded
}

func (bot *Chatbot)NicknameOverride(s *discordgo.Session, m *discordgo.MessageCreate) string {
    id := m.Author.ID

    result, ok := bot.NameOverride.Load(id)
    if ok {
        return result.(string)
    }

    go bot.GrumbleIfNicknameAndUsernameDiffer(s, m)

    return ""
}

func (bot *Chatbot)InsertNicknameGrumble(discordId string) {
    // Put it in the in-memory cache first:
    bot.GrumbledAt.Store(discordId, true)

    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()
    db := bot.Db

    now := time.Now().Format(time.RFC3339)
    stmt, err := db.Prepare("insert into discord_name_differs (`discord_id`, `row_created_at`, `row_updated_at`) values (?, ?, ?)")
    if err != nil {
        fmt.Println("Failed to retain that we already spammed", discordId, "so we will end up doing it again, reason:", err)
        return
    }
    defer stmt.Close()
    _, err = stmt.Exec(discordId, now, now)
    if err != nil {
        fmt.Println("Failed to retain that we already spammed", discordId, "so we will end up doing it again, reason:", err)
        return
    }

    return
}

func (bot *Chatbot)GrumbleIfNicknameAndUsernameDiffer(s *discordgo.Session, m *discordgo.MessageCreate) {
    _, ok := bot.GrumbledAt.Load(m.Author.ID)
    if ok {
        return
    }

    c, err := s.Channel(m.ChannelID)
    if err != nil {
        return
    }

    g, err := s.Guild(c.GuildID)
    if err != nil {
        return
    }

    for _, member := range g.Members {
        if m.Author.ID != member.User.ID {
            continue;
        }
        if member.Nick == "" {
            bot.GrumbledAt.Store(m.Author.ID, true)
            return // No nickname
        }
        nick := member.Nick
        if strings.EqualFold(nick, m.Author.Username) {
            bot.GrumbledAt.Store(m.Author.ID, true)
            return
        }

        userChannel, err := s.UserChannelCreate(m.Author.ID)
        if err != nil {
            fmt.Println("Could not ping someone about their username, error: ", err)
            return
        }

        // Okay, so their nickname differs from their username.  Have we poked them before?
        go bot.InsertNicknameGrumble(m.Author.ID)
        msg := fmt.Sprintf("To prevent abuse, the relay has to use your discord username (%s) when showing messages in KoL, not your nickname (%s).\n\nTo make it use your in-game name, send it a private message in-game with this: `/msg RelayBot Verify` and follow the instructions.", m.Author.Username, nick)
        s.ChannelMessageSend(userChannel.ID, msg)

        return
    }
}

func (bot *Chatbot)IsAdminRole(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    _, ok := bot.DiscordExtra.Administrators[m.Author.ID]
    if ok {
        return true
    }

    return false
}

func (bot *Chatbot)SenderCanRunCommands(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    if bot.IsAdminRole(s, m) {
        return true
    }

    if m.Author.Username == "hugmeir" {
        return true
    }

    return false
}

func (bot *Chatbot)SenderIsModerator(s *discordgo.Session, m *discordgo.MessageCreate) bool {
    if bot.SenderCanRunCommands(s, m) {
        return true
    }

    _, ok := bot.DiscordExtra.Moderators[m.Author.ID]
    if ok {
        return true
    }

    return false
}

type triggerTuple struct {
    re *regexp.Regexp
    cb func(*Chatbot, *discordgo.Session, *discordgo.MessageCreate, []string)
}

func HandleAliasing(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
    discordName := matches[1]
    kolName     := matches[2]

    if !bot.SenderIsModerator(s, m) {
        s.ChannelMessageSend(m.ChannelID, "Naughty members will be reported")
        return
    }

    mentions := m.Mentions
    if len(mentions) != 1 {
        s.ChannelMessageSend(m.ChannelID, "Your alias command seems broken, no clue what you meant to do")
        return
    }

    discordID := mentions[0].ID
    fmt.Printf("'%s' asked us to alias '%s' (id %s) to '%s'\n", m.Author.ID, discordName, discordID, kolName)

    go bot.InsertNewNickname(discordID, kolName)
    // Put in our in-memory hash:
    bot.NameOverride.Store(discordID, kolName)
    // Let 'em know:
    s.ChannelMessageSend(m.ChannelID, "Alias registered.  Reflect on your mistakes.")
}

var discordMessageTriggers = []triggerTuple {
    triggerTuple {
        regexp.MustCompile(`(?i)/whois\s+hugmeir`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            if m.Author.Username == "hugmeir" {
                return
            }
            s.ChannelMessageSend(m.ChannelID, "Ssh! Don't summon the accursed creator!")
        },
    },
    triggerTuple {
        regexp.MustCompile(`(?i)^\s*Relay(?:Bot)?,?\s+tell\s+me\s+about\s+(.+)`),
        func(bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            about := matches[1]
            if strings.Contains(about, "IO ERROR") {
                s.ChannelMessageSend(m.ChannelID, "Still doing that, are you?  You'll want to talk to one of _those_ other bots")
            } else if strings.Contains(about, "verification") {
                msg := "The relay uses your discord username.  To make it use your in-game name, `/msg RelayBot Verify` in-game and follow the instructions"
                s.ChannelMessageSend(m.ChannelID, msg)
                bot.KoL.SendMessage(`/clan`, msg)
            }
        },
    },
    triggerTuple {
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword) (?:alias|verif(?:y|ication)) (<@!?[0-9]+>) (?:(?:as|to) )?(.+)\z`),
        HandleAliasing,
    },

    // ALWAYS LAST
    triggerTuple {
        regexp.MustCompile(`(?i)\A!(?:cmd|powerword)\s+([a-zA-Z0-9]*)`),
        func (bot *Chatbot, s *discordgo.Session, m *discordgo.MessageCreate, matches []string) {
            cmd := matches[1]
            if cmd == "" {
                cmd = "unknown"
            }
            s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Speak loudly dear, I don't know what this '%s' command means", cmd))
        },
    },
}

func (bot *Chatbot)DiscordMessageTriggers(s *discordgo.Session, m *discordgo.MessageCreate) {
    content := m.Content

    for _, trigger := range discordMessageTriggers {
        re      := trigger.re
        matches := re.FindStringSubmatch(content)
        if len(matches) == 0 {
            continue
        }
        trigger.cb(bot, s, m, matches)
        return
    }
}

/*
{"id":"493885813935964160","channel_id":"490888267550425123","content":"\u003c@\u0026472130126482505740\u003e \u003c@289897239579459584\u003e \u003c#49088
8267550425123\u003e \u003c:catplanet:493885480971141152\u003e","timestamp":"2018-09-24T20:45:53.997000+00:00","edited_timestamp":"","mention_roles":["472130126482505740"],"tts":fals
e,"mention_everyone":false,"author":{"id":"289897239579459584","email":"","username":"hugmeir","avatar":"","discriminator":"0463","token":"","verified":false,"mfa_enabled":false,"bo
t":false},"attachments":[],"embeds":[],"mentions":[{"id":"289897239579459584","email":"","username":"hugmeir","avatar":"","discriminator":"0463","token":"","verified":false,"mfa_ena
bled":false,"bot":false}],"reactions":null,"type":0}
*/
var extraUnhandledMentions = regexp.MustCompile(`(?i)<(:[^:]+:)[0-9]+>`)

func (bot *Chatbot)ClearMoreUnhandledDiscordery(msg string) string {
    msg = extraUnhandledMentions.ReplaceAllString(msg, `$1`)
    for rank, name := range bot.DiscordExtra.RankIDToName {
        msg = strings.Replace(msg, rank, name, -1)
    }
    return msg
}

func (bot *Chatbot)HandleMessageFromDiscord(s *discordgo.Session, m *discordgo.MessageCreate, fromDiscord *os.File) {
    if m.Author.ID == s.State.User.ID {
        // Ignore ourselves
        return
    }

    relayConf         := GetRelayConf()
    targetChannel, ok := relayConf["from_discord_to_kol"][m.ChannelID]
    if !ok {
        if m.Author.Bot {
            return // nope
        }
        if dm, _ := ComesFromDM(s, m); dm {
            bot.HandleDM(s, m)
        }
        return // Someone spoke in a channel we are not relaying
    }

    msgJson, _ := json.Marshal(m)
    fmt.Fprintf(fromDiscord, "%s: %s\n", time.Now().Format(time.RFC3339), msgJson)

    msg, err := m.ContentWithMoreMentionsReplaced(s)
    if err != nil {
        msg = m.ContentWithMentionsReplaced()
    }

    msg = bot.ClearMoreUnhandledDiscordery(msg)

    if m.Attachments != nil && len(m.Attachments) > 0 {
        for _, attachment := range m.Attachments {
            if len(msg) > 0 {
                msg += " "
            }
            msg += attachment.ProxyURL
        }
    }

    if msg == "" {
        // Empty message
        // We get here when someone sends a file/picture etc
        // with no message body.  Just skip it.
        return
    }

    if bot.GlobalStfu || bot.PartialStfu {
        return // respect the desire for silence
    }

    if !m.Author.Bot {
        go bot.DiscordMessageTriggers(s, m)
    }

    author         := m.Author.Username
    authorOverride := bot.NicknameOverride(s, m)
    if authorOverride != "" && !m.Author.Bot {
        author = authorOverride
        // EXPERIMENTAL: Send messages in-game if they speak up in discord
        // But only for verified clannies!  Since presumably their names match...
        go bot.MaybeSendCarePackage(author)
    }

    author     = sanitizeForKoL(author)
    msgForKoL := sanitizeForKoL(msg)
    finalMsg  := author + ": " + msgForKoL

    kol       := bot.KoL

    if len(finalMsg) > 200 {
        // Hm..
        if len(finalMsg + author) < 300 {
            // Just split it
            kol.SendMessage(targetChannel, finalMsg[:150] + "...")
            kol.SendMessage(targetChannel, author + ": ..." + finalMsg[150:])
            return
        }
        // Too long!
        s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Brevity is the soul of wit, %s.  That message was too long, so it will not get relayed.", author))
        return
    }
    kol.SendMessage(targetChannel, finalMsg)
}

