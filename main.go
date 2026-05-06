package main

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	_ "github.com/lib/pq"
)

type Account struct {
	Email       string
	Password    string
	Name        string
	HomeworkURL string
	ChatID      string
}

type Homework struct {
	Link string `json:"link"`
	Text string `json:"text"`
}

func main() {
	// Порт для Railway
	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8080" }
		http.ListenAndServe(":"+port, nil)
	}()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" { log.Fatal("DATABASE_URL is missing") }

	db, err := sql.Open("postgres", dbURL)
	if err != nil { log.Fatal(err) }
	defer db.Close()

	// Таблица с уникальной связкой Аккаунт + Ссылка
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS saved_homeworks (
			account VARCHAR(100),
			link TEXT,
			UNIQUE(account, link)
		);`)
	if err != nil { log.Fatal(err) }

	// Настройки чатов
	myID := os.Getenv("TELEGRAM_CHAT_ID")
	otherID := os.Getenv("TELEGRAM_CHAT_ID_2")

	accounts := []Account{
		{"6probnikm@mail.ru", "goelprobe", "МАША", "https://pl.el-ed.ru/clan/5294/homeworks", myID},
		{"7probnikm@mail.ru", "goelprobe", "САША", "https://pl.el-ed.ru/clan/5293/homeworks", myID},
		{"2probnikm@mail.ru", "goelprobe", "АСЯ", "https://pl.el-ed.ru/clan/5298/homeworks", otherID},
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.WindowSize(1920, 1080),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"),
	)

	for {
		allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
		browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)

		for _, acc := range accounts {
			ctx, cancel := chromedp.NewContext(browserCtx)
			checkAccount(ctx, acc, db)
			cancel()
			time.Sleep(10 * time.Second)
		}

		cancelBrowser()
		cancelAlloc()

		fmt.Println("⏸️ Ждем 5 минут...")
		time.Sleep(5 * time.Minute)
	}
}

func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "undefined", "")
	// Убираем лишние пробелы и мусор
	re := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}

func makeHash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	var homeworks []Homework
	log.Printf("[%s] Начинаю проверку...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),
		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),
		chromedp.Sleep(8*time.Second),
		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(10*time.Second),
		// Скроллим таблицу вниз, чтобы подгрузить всё
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		chromedp.Sleep(3*time.Second),
		// Парсим строки
		chromedp.Evaluate(`
			Array.from(document.querySelectorAll('tr')).map(tr => {
				let a = tr.querySelector('a');
				return {
					link: a ? a.getAttribute("href") : "",
					text: tr.innerText
				}
			}).filter(h => h.text.length > 10)
		`, &homeworks),
	)

	if err != nil {
		log.Printf("[%s] Ошибка браузера: %v", acc.Name, err)
		return
	}

	newFound := false
	var msg []string

	for _, hw := range homeworks {
		txt := normalizeText(hw.Text)

		// Фильтры (строго маленькими буквами!)
		isMath := strings.Contains(txt, "математика") || strings.Contains(txt, "мат")
		isProb := strings.Contains(txt, "пробник") || strings.Contains(txt, "проб")

		if isMath || isProb {
			// Формируем ссылку
			finalLink := hw.Link
			if finalLink == "" || finalLink == "#" || strings.Contains(finalLink, "javascript") {
				finalLink = acc.HomeworkURL
			} else if !strings.HasPrefix(finalLink, "http") {
				finalLink = "https://pl.el-ed.ru" + finalLink
			}

			// Уникальный ключ для базы — ссылка на работу
			dbKey := finalLink 
			if dbKey == acc.HomeworkURL {
				// Если ссылки нет, создаем ключ из хэша текста
				dbKey = makeHash(acc.Name + txt)
			}

			res, err := db.Exec(`
				INSERT INTO saved_homeworks (account, link) 
				VALUES ($1, $2) 
				ON CONFLICT DO NOTHING`, acc.Name, dbKey)
			
			if err != nil { continue }

			aff, _ := res.RowsAffected()
			if aff > 0 {
				newFound = true
				cleanMsg := fmt.Sprintf("📝 *%s*\n🔗 [Открыть работу](%s)", hw.Text, finalLink)
				msg = append(msg, cleanMsg)
			}
		}
	}

	if newFound {
		botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
		text := "🔥 *" + acc.Name + "*\nНовые работы:\n\n" + strings.Join(msg, "\n\n---\n")
		sendTelegram(botToken, acc.ChatID, text)
		log.Printf("[%s] Отправлено уведомление", acc.Name)
	} else {
		log.Printf("[%s] Ничего нового", acc.Name)
	}
}

func sendTelegram(token, chatID, message string) {
	if token == "" || chatID == "" { return }
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	
	http.PostForm(apiURL, url.Values{
		"chat_id":    {chatID},
		"text":       {message},
		"parse_mode": {"Markdown"},
	})
}