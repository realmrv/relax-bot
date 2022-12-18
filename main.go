package main

import (
	"errors"
	"github.com/rollbar/rollbar-go"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"os"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

import _ "github.com/joho/godotenv/autoload"

type User struct {
	gorm.Model
	TgID      int64
	Username  string     `gorm:"uniqueIndex"`
	Keywords  []*Keyword `gorm:"many2many:user_keywords;"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (u *User) Recipient() string {
	return strconv.FormatInt(u.TgID, 10)
}

type Keyword struct {
	gorm.Model
	Name      string  `gorm:"uniqueIndex"`
	Users     []*User `gorm:"many2many:user_keywords;"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func main() {
	rollbar.SetToken(os.Getenv("ROLLBAR_TOKEN"))
	rollbar.SetEnvironment(os.Getenv("APP_ENV"))
	rollbar.SetCodeVersion(os.Getenv("ROLLBAR_CODE_VERSION"))
	rollbar.SetServerHost(os.Getenv("ROLLBAR_HOST"))
	rollbar.SetServerRoot(os.Getenv("ROLLBAR_ROOT"))

	db, err := gorm.Open(sqlite.Open("main.db"), &gorm.Config{})
	if err != nil {
		rollbar.LogPanic(err, true)
		panic(errors.New("failed to connect database"))
	}

	err = db.AutoMigrate(&User{}, &Keyword{})
	if err != nil {
		rollbar.LogPanic(err, true)
		panic(errors.New("failed to auto migrate database"))
	}

	var keywords = []Keyword{{Name: "#го"}, {Name: "#знакомство"}, {Name: "#рекомендую"}}
	db.Clauses(clause.OnConflict{DoNothing: true}).Create(&keywords)

	pref := tele.Settings{
		Token:  os.Getenv("TOKEN"),
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		rollbar.LogPanic(err, true)
		panic(errors.New("failed to build bot"))
	}

	b.Handle("/hello", func(c tele.Context) error {
		return c.Send("Hello")
	})

	b.Handle("/start", func(c tele.Context) error {
		if c.Chat().Type != tele.ChatPrivate {
			return nil
		}

		var user = User{Username: c.Sender().Username, TgID: c.Sender().ID}
		result := db.First(&user, &user)
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return c.Send("You are already in the party!")
		}
		db.Unscoped().FirstOrCreate(&user, &user)
		if user.DeletedAt.Valid {
			db.Unscoped().Model(&user).Update("deleted_at", nil)
		}
		var keywords []Keyword
		db.Find(&keywords)
		err := db.Model(&user).Association("Keywords").Replace(&keywords)
		if err != nil {
			rollbar.Error(err)
			return err
		}

		rollbar.Info("Added user: " + user.Username)
		return c.Send("Hello! Now you will receive notifications as soon as a message with a hashtag appears.")
	})

	b.Handle("/stop", func(c tele.Context) error {
		if c.Chat().Type != tele.ChatPrivate {
			return nil
		}

		var user = User{Username: c.Sender().Username, TgID: c.Sender().ID}
		db.Delete(&user, &user)

		rollbar.Info("Deleted user: " + user.Username)
		return c.Send("Bye. Notifications won't bother you anymore.")
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		if c.Chat().Type == tele.ChatPrivate {
			return nil
		}

		var keywords []Keyword

		err := db.Model(&Keyword{}).Preload("Users").Find(&keywords).Error
		if err != nil {
			rollbar.Error(err)
		}

		for _, keyword := range keywords {
			if strings.Contains(c.Message().Text, keyword.Name) {
				for _, user := range keyword.Users {
					var recipient tele.Recipient = user
					err := c.ForwardTo(recipient)
					if err != nil {
						rollbar.Warning(err)
					}
				}
			}
		}
		return nil
	})

	b.Start()
	rollbar.Wait()
}
