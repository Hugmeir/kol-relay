package main

import (
    "os"
    "regexp"
    "fmt"
    "strings"
    "bytes"
    "time"
    "sync"
    "github.com/Hugmeir/kolgo"
    "database/sql"
)

const MAX_TITLE_LENGTH = 40

/*
<select name="level2331078"><option value="0">Normal Member (°0)</option><option value="8">Debtor's Prison (°1)</option><option value="14">Dead Fish (°2)</option><option value="7">Inactive (°4)</option><option value="9">Fresh Fish (°6)</option><option value="18">Silent Pleasure (°25)</option><option value="5" selected="">Pleasure Seeker (°50)</option></select>
*/
var rankNameToID = map[string]string{
    "Normal Member":   "0",
    "Pleasure Seeker": "5",
    "Inactive":        "7",
    "Debtor's Prison": "8",
    "Fresh Fish":      "9",
    "Dead Fish":       "14",
    "Silent Pleasure": "18",
}

var activeRankToInactive = map[string]string{
    "Pleasure Seeker": "Inactive",
    "Fresh Fish":      "Dead Fish",
}
var inactiveRankToActive = map[string]string{
    "Inactive":  "Pleasure Seeker",
    "Dead Fish": "Fresh Fish",
}

type handlerInterface func(ClanApplication)
type ToilBot struct {
    KoL       kolgo.KoLRelay
    GoogleSheetsConfig *GoogleSheetsConfig
    BlackList sync.Map
    Handlers  sync.Map
    Stop      bool
}

func (toilbot *ToilBot)InsertBlacklist(name string, id string, reason string, db *sql.DB) {
    // Try to skip the slightly slower SQL operation:
    if id != "" {
        if _, ok := toilbot.BlackList.Load("ID:" + id); ok {
            return
        }
    }
    if name != "" {
        if _, ok := toilbot.BlackList.Load("Name:" + strings.ToLower(name)); ok {
            return
        }
    }

    toilbot.BlackList.Store("Name:" + strings.ToLower(name), true)
    toilbot.BlackList.Store("ID:"   + id,                    true)

    sqliteInsert.Lock()
    defer sqliteInsert.Unlock()

    now := time.Now().Format(time.RFC3339)
    stmt, err := db.Prepare("INSERT INTO kol_blacklist (`unique_ident`, `account_name`, `account_number`, `reason`, `row_created_at`, `row_updated_at`) VALUES (?, ?, ?, ?, ?, ?)")
    if err != nil {
        fmt.Println("Failed to prepare an update for the local blacklist: ", err)
        return
    }
    defer stmt.Close()

    _, err = stmt.Exec(id + "|" + name, name, id, reason, now, now)
    if err != nil {
        if strings.Contains(err.Error(), `UNIQUE constraint failed`) {
            return
        }
        fmt.Println("Failed to store to the local blacklist: ", err)
        return
    }
    return
}

func NewToilBot(username string, password string, db *sql.DB, conf *GoogleSheetsConfig) *ToilBot {
    kol := kolgo.NewKoL(username, password, nil)

    err := kol.LogIn()
    if err != nil {
        panic(err)
    }

    bot := &ToilBot{
        KoL:                kol,
        GoogleSheetsConfig: conf,
        Stop:               false,
    }

    rows, err := db.Query("SELECT account_name, account_number FROM kol_blacklist")
    if err != nil {
        bot.Stop = true
        return bot
    }
    defer rows.Close()
    for rows.Next() {
        var playerName  string
        var playerID    string
        err = rows.Scan(&playerName, &playerID)
        if err != nil {
            fmt.Println(err)
            continue
        }
        // No need to foldcase
        bot.BlackList.Store("Name:" + strings.ToLower(playerName), true)
        bot.BlackList.Store("ID:"   + playerID,                    true)
    }

    return bot
}

const FCA_FreshFish = "9"
const FCA_WELCOME = `Hi, and welcome to FCA!

Come hang out in chat (type '/c clan' in the chat pane) to get a title and get ranked up to Pleasure Seeker.

Once you are ranked up, you'll be able to access the clan stash and clan dungeon, and you'll automatically get a whitelist too!  Please read the rules for dungeon use in the clan forum, or ask in chat.

Feel free to join the clan Discord: https://discord.gg/CmSfAgq`

const FCA_AnnounceKoLFmt = `Player --> %s (#%s) was just accepted to the clan.`

func (toil *ToilBot)BlacklistedPlayer(n string, id string) bool {
    bl := toil.BlackList

    // Blacklisted ID?
    if _, ok := bl.Load("ID:" + id); ok {
        return true
    }

    n = strings.ToLower(n)
    if _, ok := bl.Load("Name:" + n); ok {
        return true
    }

    n = strings.Replace(n, ` `, `_`, -1)
    if _, ok := bl.Load("Name:" + n); ok {
        return true
    }

    n = strings.Replace(n, `_`, ` `, -1)
    if _, ok := bl.Load("Name:" + n); ok {
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
        if toilbot.BlacklistedPlayer(app.PlayerName, app.PlayerID) {
            fmt.Printf("REJECTING application from blacklisted user %s\n", app.PlayerName)
            _, err := kol.ClanProcessApplication(app.RequestID, false)
            if err != nil {
                fmt.Println("Unable to reject application: ", err)
            }
            continue
        }

        _, month, day := time.Now().Date()
        title := fmt.Sprintf("%02d/%02d awaiting Naming Day", int(month), day)

        body, err := kol.ClanProcessApplication(app.RequestID, true)
        if err != nil {
            fmt.Println("Failed to accept an application: ", err)
            continue
        }

        if bytes.Contains(body, []byte(`You cannot accept new members into the clan.`)) {
            fmt.Println("No permissions to accept new clannies")
            toilbot.Stop = true
            return
        }

        f, err := os.OpenFile("/tmp/kol_applications", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
        if err == nil {
            defer f.Close()
            f.Write(body)
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

const FCA_PleasureSeekerID   = `5` // Pleasure Seeker
const FCA_PleasureSeekerName = `Pleasure Seeker`
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
        member.Rank = FCA_PleasureSeekerName

        mods = append(mods, kolgo.ClanMemberModification{
            ID:     member.ID,
            RankID: FCA_PleasureSeekerID,
            Title:  member.Title,
        })
    }

    if len(mods) == 0 {
        return
    }

    kol := toilbot.KoL
    body, err := kol.ClanModifyMembers(mods)
    if err != nil {
        return
    }
    // TODO: check if body contains the thing we need
    if body == nil {
        return
    }
    return
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
    clanniesToUnwhitelist := map[string]bool{}
    for _, wl := range whitelisted {
        if toilbot.BlacklistedPlayer(wl.Name, wl.ID) {
            // Huh.
            clanniesToUnwhitelist[wl.ID] = true
        } else {
            clanniesWhitelisted[wl.ID] = true
        }
    }

    for _, m := range clannies {
        if _, ok := clanniesWhitelisted[m.ID]; ok {
            continue
        }

        // If they are back in, someone manually approved them, so
        // assume that they should stay
        delete(clanniesToUnwhitelist, m.ID)

        if m.Rank != FCA_PleasureSeekerName {
            continue
        }

        mods = append(mods, m)
    }

    // Remove anyone blacklisted from the whitelist.  Happens rarely, but...
    for id, _ := range clanniesToUnwhitelist {
        time.Sleep(5 * time.Second)
        bot.KoL.ClanRemoveWhitelist(id)
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
        if !bytes.Contains(body, []byte(`added to whitelist`)) {
            fmt.Println("Failed to whitelist: ", member.Name, string(body))
        }
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

    // Sequence: Upgrade to pleasure seeker *first*, then
    // do all the other stuff async
    toilbot.UpgradeSilentPleasures(clannies)
    go toilbot.EnsureAllSeekersAreWhitelisted(bot, clannies)
    go toilbot.CheckActivesAndInactives(clannies)
}

func (toilbot *ToilBot) CheckActivesAndInactives(clannies []ClanMember) {
    mods := make([]kolgo.ClanMemberModification, 0, 10)

    _, month, _ := time.Now().Date()
    for _, member := range clannies {
        if member.Inactive {
            if _, ok := inactiveRankToActive[member.Rank]; ok {
                // Already marked as inactive, just carry on
                continue
            }
            newRank, ok := activeRankToInactive[member.Rank]
            if !ok {
                // We are not handling these.
                continue
            }

            rankID, ok := rankNameToID[newRank]
            if !ok {
                fmt.Println("Rank misconfigured for ", newRank)
                continue
            }

            newTitle := fmt.Sprintf("%d - %s", month, member.Title)
            if len(newTitle) > 40 {
                newTitle = newTitle[:40]
            }
            // newly inactive clannie.  Sadness.
            mods = append(mods, kolgo.ClanMemberModification{
                ID:     member.ID,
                RankID: rankID,
                Title:  newTitle,
            })
        } else {
            newRank, ok := inactiveRankToActive[member.Rank]
            if !ok {
                // Active and in an active rank.
                continue
            }

            // Ooh, active but with an inactive rank.  Welcome back!
            newRankID, ok := rankNameToID[newRank]
            if !ok {
                fmt.Println("Misconfigured rank: ", newRank)
                continue
            }

            re    := regexp.MustCompile(`\A[0-9]{1,2}\s?-\s?`)
            title := re.ReplaceAllString(member.Title, ``)
            mods = append(mods, kolgo.ClanMemberModification{
                ID:     member.ID,
                RankID: newRankID,
                Title:  title,
            })
        }
    }

    if len(mods) == 0 {
        return
    }

    kol := toilbot.KoL
    body, err := kol.ClanModifyMembers(mods)
    if err != nil {
        return
    }
    // TODO: check if body contains the thing we need
    if body == nil {
        return
    }
    return
}

func (toilbot *ToilBot)MaintainBlacklist(bot *Chatbot) {
    blacklist := ReadBlacklist(toilbot.GoogleSheetsConfig)
    for _, bl := range blacklist {
        toilbot.InsertBlacklist(bl.Name, bl.ID, bl.Reason, bot.Db)
    }
}

func (toilbot *ToilBot)PollClanManagement(bot *Chatbot) {
    blacklistTicker    := time.NewTicker(23 * time.Minute)
    applicationsTicker := time.NewTicker(5  * time.Minute)
    memberRankTicker   := time.NewTicker(2  * time.Hour)
    defer blacklistTicker.Stop()
    defer applicationsTicker.Stop()
    defer memberRankTicker.Stop()
    defer func() { fmt.Println("No longer polling for new applications") }()
    for {
        if toilbot.Stop {
            return
        }
        select {
            case <-blacklistTicker.C:
                go toilbot.MaintainBlacklist(bot)
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
