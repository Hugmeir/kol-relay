package main

import (
    "fmt"
    "strings"
    "bytes"
    "time"
    "sync"
    "github.com/Hugmeir/kolgo"
    "database/sql"
)

type handlerInterface func(ClanApplication)
type ToilBot struct {
    KoL       kolgo.KoLRelay
    BlackList sync.Map
    Handlers  sync.Map
    Stop      bool
}

func NewToilBot(username string, password string, db *sql.DB) *ToilBot {
    kol := kolgo.NewKoL(username, password, nil)

    err := kol.LogIn()
    if err != nil {
        panic(err)
    }

    bot := &ToilBot{
        KoL: kol,
        Stop: false,
    }

    rows, err := db.Query("SELECT player_name FROM kol_blacklist")
    if err != nil {
        bot.Stop = true
        return bot
    }
    defer rows.Close()
    for rows.Next() {
        var playerName  string
        err = rows.Scan(&playerName)
        if err != nil {
            fmt.Println(err)
            continue
        }
        bot.BlackList.Store(playerName, true)
    }

    return bot
}

const FCA_FreshFish = "9"
const FCA_WELCOME = `Hi, and welcome to FCA!

Come hang out in chat (type '/c clan' in the chat pane) to get a title and get ranked up to Pleasure Seeker.

Once you are ranked up, you'll be able to access the clan stash and clan dungeon, and you'll automatically get a whitelist too!  Please read the rules for dungeon use in the clan forum, or ask in chat.

Feel free to join the clan Discord: https://discord.gg/CmSfAgq`

const FCA_AnnounceKoLFmt = `Player --> %s (#%s) was just accepted to the clan.`

func (toil *ToilBot)BlacklistedName(n string) bool {
    n = strings.ToLower(n)
    if _, ok := toil.BlackList.Load(n); ok {
        return true
    }

    n = strings.Replace(n, ` `, `_`, -1)
    if _, ok := toil.BlackList.Load(n); ok {
        return true
    }

    n = strings.Replace(n, `_`, ` `, -1)
    if _, ok := toil.BlackList.Load(n); ok {
        return true
    }

    return false
}

const (
    AcceptedApplication = "Accept"
)

func (toilbot *ToilBot) CheckNewApplications(bot *Chatbot) {
    body, err := bot.KoL.ClanApplications()
    if err != nil {
        fmt.Println("Unable to poll for new applications: ", err)
        return
    }
    applications := DecodeClanApplications(body)
    if len(applications) < 1 {
        return
    }

    // Okay, we got applications.  So far we've been using relaybot to
    // do the polling, but that was just to save resources.  To do
    // proper clan management, we need to use the toilbot:
    kol := toilbot.KoL
    for _, app := range applications {
        if toilbot.BlacklistedName(app.PlayerName) {
            fmt.Printf("REJECTING application from blacklisted user %s\n", app.PlayerName)
            _, err := kol.ClanProcessApplication(app.RequestID, false)
            if err != nil {
                if fatalError := kol.HandleKoLException(err); fatalError != nil {
                    fmt.Println("Unable to reject application: ", err)
                    continue
                }
            }
            continue
        }

        _, month, day := time.Now().Date()
        title := fmt.Sprintf("%02d/%02d awaiting Naming Day", int(month), day)

        body, err := kol.ClanProcessApplication(app.RequestID, true)
        if err != nil {
            if err := kol.HandleKoLException(err); err != nil {
                fmt.Println("Failed to accept an application: ", err)
                continue
            }
            // Okay, so that previous attempt failed, try one more time:
            body, err = kol.ClanProcessApplication(app.RequestID, true)
            if err != nil {
                // Failed again.  Sorry new person, you'll get skipped for now.
                fmt.Println("Failed to accept an application: ", err)
                continue
            }
        }
        if bytes.Contains(body, []byte(`You cannot accept new members into the clan.`)) {
            toilbot.Stop = true
            return
        }
        clannies := []kolgo.ClanMemberModification{
            kolgo.ClanMemberModification{
                ID:     app.PlayerID,
                RankID: FCA_FreshFish,
                Title:  title,
            },
        }
        // TODO: use the bulk version!!
        body, err = kol.ClanModifyMembers(clannies)
        if err != nil {
            fmt.Println("Failed to process an application: ", err)
            continue
        }

        cbs, ok := toilbot.Handlers.Load(AcceptedApplication)
        if !ok {
            continue
        }

        for _, cb := range cbs.([]handlerInterface) {
            go cb(app)
        }
    }
}

const FCA_PleasureSeeker = `5` // Pleasure Seeker
const upgradeRank = `Silent Pleasure`
func (toilbot *ToilBot) UpgradeSilentPleasures(clannies []ClanMember) {
    mods := make([]kolgo.ClanMemberModification, 0, 10)

    for _, member := range clannies {
        if member.Rank != upgradeRank {
            continue
        }
        if member.Title == "" {
            continue // We probably failed to parse it because of bullshit inconsistent rules
        }

        // Well... they will be, soon enough.
        member.Rank = FCA_PleasureSeeker

        mods = append(mods, kolgo.ClanMemberModification{
            ID:     member.ID,
            RankID: FCA_PleasureSeeker,
            Title:  member.Title,
        })
    }

    if len(mods) == 0 {
        return
    }

    kol := toilbot.KoL
    body, err := kol.ClanModifyMembers(mods)
    if err != nil {
        if fatalError := kol.HandleKoLException(err); fatalError != nil {
            return
        }
        body, err = kol.ClanModifyMembers(mods)
        if err != nil {
            return
        }
    }

    // TODO: check if body contains the thing we need
    if body != nil {
        return
    }
}

func (toilbot *ToilBot) EnsureAllSeekersAreWhitelisted(bot *Chatbot, clannies []ClanMember) {
    mods := make([]ClanMember, 0, 10)

    body, err := bot.KoL.ClanWhitelist()
    if err != nil {
        fmt.Println("Could not get the whitelist: ", err)
        return
    }

    whitelisted := DecodeClanWhitelist(body)
    clanniesWhitelisted := map[string]bool{}
    for _, wl := range whitelisted {
        clanniesWhitelisted[wl.ID] = true
    }

    for _, m := range clannies {
        if _, ok := clanniesWhitelisted[m.ID]; ok {
            continue
        }
        if m.Rank != FCA_PleasureSeeker {
            continue
        }

        fmt.Println("Would have whitelisted ", m.Name)
        //mods = append(mods, m)
    }

    if len(mods) == 0 {
        return
    }

    kol := toilbot.KoL
    for _, member := range mods {
        time.Sleep(5 * time.Second) // No need to rush
        body, err := kol.ClanAddWhitelist(member.Name, member.Rank, member.Title)
        if err != nil {
            fmt.Println("Failed to whitelist: ", err, string(body))
        }
        // TODO: check that body contains the 'we did it!' string
    }
}

func (toilbot *ToilBot) CheckMemberRankChanges(bot *Chatbot) {
    page1, err := bot.KoL.ClanMembers(1)
    if err != nil {
        fmt.Println("Error polling for clan members: ", err)
        return
    }

    clannies := DecodeClanMembers(page1)

    totalPages := DecodeTotalMemberPages(page1)

    for i := 2; i <= totalPages; i++ {
        // We aren't in a rush, spread out the queries:
        time.Sleep(5 * time.Second)
        page, err := bot.KoL.ClanMembers(i)
        if err != nil {
            fmt.Printf("Could not query members page %d: %s", i, err)
            continue
        }
        members := DecodeClanMembers(page)
        clannies = append(clannies, members...)
    }

    // These two happen sequentially for good reasons:
    toilbot.UpgradeSilentPleasures(clannies)
    go toilbot.EnsureAllSeekersAreWhitelisted(bot, clannies)

    // TODO: inactives
    // TODO: re-actives
}

func (toilbot *ToilBot)PollClanManagement(bot *Chatbot) {
    applicationsTicker := time.NewTicker(5 * time.Minute)
    memberRankTicker   := time.NewTicker(2 * time.Hour)
    defer applicationsTicker.Stop()
    defer memberRankTicker.Stop()
    defer func() { fmt.Println("No longer polling for new applications") }()
    for {
        if toilbot.Stop {
            return
        }
        select {
            case <-applicationsTicker.C:
                go toilbot.CheckNewApplications(bot)
            case <-memberRankTicker.C:
                go toilbot.CheckMemberRankChanges(bot)
    } }
}

func (toil *ToilBot)AddHandler(eventType string, cb handlerInterface) {
    handlers, ok := toil.Handlers.Load(eventType)
    if ok {
        toil.Handlers.Store(eventType, append(handlers.([]handlerInterface), cb))
    } else {
        toil.Handlers.Store(eventType, []handlerInterface{cb})
    }
}
