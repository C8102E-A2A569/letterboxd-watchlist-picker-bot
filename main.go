package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gocolly/colly/v2"
	"github.com/joho/godotenv"
)

type Film struct {
	ID    string
	Title string
	Year  string
	URL   string
}

// UserState представляет состояние пользователя для диалогов
type UserState struct {
	CurrentUsername  string
	CurrentState     string // Текущее состояние: "", "username", "listUsername", "listname", "randomcount"
	TempListUsername string // Временное хранение имени держателя списка для команды /list
}

// Глобальный словарь состояний пользователей
var userStates = make(map[int64]*UserState)

// Константы для состояний пользователя
const (
	StateNone         = ""
	StateUsername     = "username"
	StateListUsername = "listUsername"
	StateListName     = "listname"
	StateRandomCount  = "randomcount"
)

// Форматирование имени пользователя в нижний регистр
func formatUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// Форматирование названия списка в нижний регистр, пробелы заменяются на тире
func formatListName(listName string) string {
	listName = strings.ToLower(strings.TrimSpace(listName))
	return strings.ReplaceAll(listName, " ", "-")
}

// Получение watchlist пользователя Letterboxd
func getLetterboxdWatchlist(username string) ([]Film, error) {
	return getLetterboxdList(username, "watchlist")
}

// Получение публичного списка пользователя Letterboxd
func getLetterboxdList(username, listName string) ([]Film, error) {
	var films []Film

	// Форматируем имя пользователя и название списка
	username = formatUsername(username)
	listName = formatListName(listName)

	c := colly.NewCollector()

	var responseReceived bool

	c.OnHTML(".poster-container", func(e *colly.HTMLElement) {
		responseReceived = true

		film := Film{
			ID:    e.Attr("data-film-id"),
			Title: e.ChildAttr(".film-poster", "alt"),
			URL:   "https://letterboxd.com" + e.ChildAttr("div.film-poster", "data-target-link"),
		}

		year := e.ChildAttr(".film-poster", "data-film-release-year")
		if year != "" {
			film.Year = year
		}

		films = append(films, film)
	})

	// Проверка существования списка или пользователя
	c.OnHTML("body", func(e *colly.HTMLElement) {
		responseReceived = true
	})

	c.OnError(func(r *colly.Response, err error) {
		log.Println("Ошибка при запросе:", err)
	})

	var url string
	if listName == "watchlist" {
		url = fmt.Sprintf("https://letterboxd.com/%s/watchlist/", username)
	} else {
		url = fmt.Sprintf("https://letterboxd.com/%s/list/%s/", username, listName)
	}

	err := c.Visit(url)
	if err != nil {
		return nil, fmt.Errorf("не удалось подключиться к Letterboxd: %v", err)
	}

	if !responseReceived {
		return nil, fmt.Errorf("ответ от сервера не получен")
	}

	return films, nil
}

// Получение случайных фильмов из списка
func getRandomFilms(films []Film, count int) []Film {
	if count > len(films) {
		count = len(films)
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Перемешиваем фильмы
	r.Shuffle(len(films), func(i, j int) {
		films[i], films[j] = films[j], films[i]
	})

	return films[:count]
}

// Форматирование списка фильмов в текст сообщения
func formatFilmsResponse(films []Film, originalListName, username string) string {
	if len(films) == 1 {
		film := films[0]
		year := ""
		if film.Year != "" {
			year = " (" + film.Year + ")"
		}

		if originalListName != "watchlist" {
			return fmt.Sprintf("Рандомный фильм из списка '%s' пользователя %s:\n\n%s%s\n%s",
				originalListName, username, film.Title, year, film.URL)
		}
		return fmt.Sprintf("Рандомный фильм из вашего Watchlist:\n\n%s%s\n%s", film.Title, year, film.URL)
	}

	var result strings.Builder
	if originalListName != "watchlist" {
		result.WriteString(fmt.Sprintf("Рандомные фильмы из списка '%s' пользователя %s:\n\n", originalListName, username))
	} else {
		result.WriteString("Рандомные фильмы из вашего Watchlist:\n\n")
	}

	for i, film := range films {
		year := ""
		if film.Year != "" {
			year = " (" + film.Year + ")"
		}
		result.WriteString(fmt.Sprintf("%d. %s%s\n%s\n\n", i+1, film.Title, year, film.URL))
	}

	return strings.TrimSuffix(result.String(), "\n\n")
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Не удалось загрузить .env, использую системные переменные")
	}

	// Инициализация бота
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не установлен")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Бот авторизован как %s", bot.Self.UserName)

	// Настройка обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	// Обработка обновлений
	for update := range updates {
		if update.Message == nil {
			continue
		}

		// Получаем текущее состояние пользователя
		userID := update.Message.From.ID
		state, exists := userStates[userID]
		if !exists {
			state = &UserState{CurrentState: StateNone}
			userStates[userID] = state
		}

		// Проверяем на команду /cancel во всех случаях
		if update.Message.IsCommand() && update.Message.Command() == "cancel" {
			state.CurrentState = StateNone
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Команда отменена. Вы можете начать заново.")
			bot.Send(msg)
			continue
		}

		// Обработка текстовых сообщений при ожидании ввода от пользователя
		if state.CurrentState != StateNone && update.Message.Text != "" && !update.Message.IsCommand() {
			switch state.CurrentState {
			case StateUsername:
				username := formatUsername(update.Message.Text)
				state.CurrentUsername = username
				state.CurrentState = StateNone

				msg := tgbotapi.NewMessage(update.Message.Chat.ID,
					fmt.Sprintf("Имя пользователя Letterboxd установлено: %s", username))
				bot.Send(msg)

			case StateListUsername:
				// Сохраняем имя держателя списка во временной переменной
				listUsername := formatUsername(update.Message.Text)
				state.TempListUsername = listUsername
				state.CurrentState = StateListName

				msg := tgbotapi.NewMessage(update.Message.Chat.ID,
					fmt.Sprintf("Имя держателя списка установлено: %s\nТеперь введите название публичного списка.", listUsername))
				bot.Send(msg)

			case StateListName:
				originalListName := update.Message.Text
				formattedListName := formatListName(originalListName)
				listUsername := state.TempListUsername // Используем имя держателя списка
				state.CurrentState = StateNone

				// Получаем список
				films, err := getLetterboxdList(listUsername, formattedListName)
				if err != nil {
					log.Printf("Ошибка при получении списка: %v", err)
					errorMsg := tgbotapi.NewMessage(update.Message.Chat.ID,
						fmt.Sprintf("Не удалось получить список фильмов. Возможные причины:\n"+
							"- Пользователь '%s' не найден\n"+
							"- Список '%s' не существует\n"+
							"- Список является приватным", listUsername, originalListName))
					bot.Send(errorMsg)
					continue
				}

				if len(films) == 0 {
					emptyMsg := tgbotapi.NewMessage(update.Message.Chat.ID,
						fmt.Sprintf("Список '%s' пользователя '%s' пуст или не содержит фильмов.",
							originalListName, listUsername))
					bot.Send(emptyMsg)
					continue
				}

				// Получаем случайный фильм
				randomFilms := getRandomFilms(films, 1)
				responseText := formatFilmsResponse(randomFilms, originalListName, listUsername)

				msg := tgbotapi.NewMessage(update.Message.Chat.ID, responseText)
				msg.DisableWebPagePreview = false
				bot.Send(msg)

			case StateRandomCount:
				count, err := strconv.Atoi(strings.TrimSpace(update.Message.Text))
				state.CurrentState = StateNone

				if err != nil || count < 1 {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Пожалуйста, введите корректное число (целое положительное число).")
					bot.Send(msg)
					continue
				}

				if count > 10 {
					count = 10
				}

				if state.CurrentUsername == "" {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Сначала установите имя пользователя Letterboxd с помощью команды /set_username.")
					bot.Send(msg)
					continue
				}

				// Получаем watchlist
				films, err := getLetterboxdWatchlist(state.CurrentUsername)
				if err != nil {
					log.Printf("Ошибка при получении watchlist: %v", err)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID,
						fmt.Sprintf("Не удалось получить watchlist. Возможные причины:\n"+
							"- Пользователь '%s' не найден\n"+
							"- Watchlist является приватным", state.CurrentUsername))
					bot.Send(msg)
					continue
				}

				if len(films) == 0 {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Ваш Watchlist пуст или не содержит фильмов.")
					bot.Send(msg)
					continue
				}

				// Получаем случайные фильмы
				randomFilms := getRandomFilms(films, count)
				responseText := formatFilmsResponse(randomFilms, "watchlist", state.CurrentUsername)

				msg := tgbotapi.NewMessage(update.Message.Chat.ID, responseText)
				msg.DisableWebPagePreview = false
				bot.Send(msg)
			}

			continue
		}

		// Обработка команд
		if update.Message.IsCommand() {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

			switch update.Message.Command() {
			case "help":
				msg.Text = "Список доступных команд:\n\n" +
					"/start - Начать работу с ботом\n" +
					"/help - Показать справку по командам\n" +
					"/cancel - Отменить текущую команду\n" +
					"/set_username - Установить имя пользователя Letterboxd\n" +
					"/random - Получить случайный фильм из вашего Watchlist\n" +
					"/random_n - Получить N случайных фильмов (где N от 1 до 10)\n" +
					"/list - Получить случайный фильм из публичного списка\n"

			case "start":
				msg.Text = "Добро пожаловать в Letterboxd Watchlist Picker Bot! 🎬\n\n" +
					"Я помогу выбрать случайный фильм из вашего Watchlist или из любого публичного списка.\n\n" +
					"Чтобы начать, установите свое имя пользователя Letterboxd с помощью команды /set_username\n\n" +
					"Для справки используйте /help"

			case "set_username":
				msg.Text = "Введите ваше имя пользователя Letterboxd."
				state.CurrentState = StateUsername

			case "list":
				msg.Text = "Введите имя пользователя Letterboxd, чей список вы хотите просмотреть."
				state.CurrentState = StateListUsername

			case "random_n":
				if state.CurrentUsername == "" {
					msg.Text = "Сначала установите имя пользователя Letterboxd с помощью команды /set_username."
					break
				}

				msg.Text = "Введите количество рандомных фильмов (от 1 до 10)."
				state.CurrentState = StateRandomCount

			case "random":
				if state.CurrentUsername == "" {
					msg.Text = "Сначала установите имя пользователя Letterboxd с помощью команды /set_username."
					break
				}

				films, err := getLetterboxdWatchlist(state.CurrentUsername)
				if err != nil {
					log.Printf("Ошибка при получении watchlist: %v", err)
					msg.Text = fmt.Sprintf("Не удалось получить watchlist. Возможные причины:\n"+
						"- Пользователь '%s' не найден\n"+
						"- Watchlist является приватным", state.CurrentUsername)
					break
				}

				if len(films) == 0 {
					msg.Text = "Ваш Watchlist пуст или не содержит фильмов."
					break
				}

				randomFilms := getRandomFilms(films, 1)
				msg.Text = formatFilmsResponse(randomFilms, "watchlist", state.CurrentUsername)
				msg.DisableWebPagePreview = false
			}

			if _, err := bot.Send(msg); err != nil {
				log.Println(err)
			}
		}
	}
}
