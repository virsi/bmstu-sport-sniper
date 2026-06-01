// Package bot — обёртка над telebot.v3 для notifier-svc.
//
// Поддерживает long-poll (по умолчанию) и webhook режим. Слой логики
// разделён через интерфейс Sender — это позволяет unit-тестить server без
// настоящего TG-бота.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

// Sender — минимальный интерфейс отправки сообщения в Telegram chat.
//
// Реализуется *Bot для реального TG и тестовыми/моковыми типами в тестах.
// Метод НЕ принимает контекст: telebot.v3 не поддерживает контекстную отмену
// на send (внутри сетка с долгим timeout).
type Sender interface {
	// SendHTML отправляет HTML-форматированный текст в chat.
	// chatID — telegram chat_id (int64). DisablePreview = true.
	SendHTML(chatID int64, text string) error
}

// LinkCompleter завершает Telegram-привязку через auth-svc.
//
// Реализация лежит в server-слое (вызов AuthService.LinkTelegramComplete),
// здесь — только интерфейс для cmd-handler.
type LinkCompleter interface {
	// Complete привязывает chatID к user_id по token. Возвращает user_id
	// или ошибку (NotFound/Expired).
	Complete(ctx context.Context, token string, chatID int64) (userID string, err error)
}

// Config — параметры запуска бота.
type Config struct {
	// Token — токен от @BotFather (обязателен).
	Token string
	// UseWebhook — true → webhook, false → long-poll.
	UseWebhook bool
	// WebhookURL — публичный URL для setWebhook (если UseWebhook).
	WebhookURL string
	// PollerTimeout — таймаут long-poller'а (по умолчанию 10s).
	PollerTimeout time.Duration
}

// Bot — рантайм telebot.v3 + LinkCompleter для /start <token>.
type Bot struct {
	b      *tele.Bot
	logger *slog.Logger
	linker LinkCompleter
}

// SetLinker устанавливает LinkCompleter после создания бота.
// Используется в main, где Server (реализует LinkCompleter) и Bot
// циклически ссылаются друг на друга.
func (b *Bot) SetLinker(l LinkCompleter) { b.linker = l }

// New создаёт и настраивает Bot, регистрирует команды.
// linker может быть nil — тогда /start <token> вернёт юзеру generic-ответ
// (полезно в health-only сценариях). Используйте SetLinker для поздней инъекции.
func New(cfg Config, linker LinkCompleter, logger *slog.Logger) (*Bot, error) {
	if cfg.Token == "" {
		return nil, errors.New("bot: empty TG token")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.PollerTimeout <= 0 {
		cfg.PollerTimeout = 10 * time.Second
	}

	settings := tele.Settings{
		Token: cfg.Token,
	}
	if cfg.UseWebhook {
		settings.Poller = &tele.Webhook{
			Listen:   ":8443",
			Endpoint: &tele.WebhookEndpoint{PublicURL: cfg.WebhookURL},
		}
	} else {
		settings.Poller = &tele.LongPoller{Timeout: cfg.PollerTimeout}
	}

	tb, err := tele.NewBot(settings)
	if err != nil {
		return nil, fmt.Errorf("bot: telebot init: %w", err)
	}

	bot := &Bot{
		b:      tb,
		logger: logger,
		linker: linker,
	}
	bot.registerHandlers()
	return bot, nil
}

// registerHandlers подвязывает /start, /help, /status.
func (b *Bot) registerHandlers() {
	b.b.Handle("/start", b.handleStart)
	b.b.Handle("/help", b.handleHelp)
	b.b.Handle("/status", b.handleStatus)
}

// handleStart обрабатывает /start [token].
//
// Сценарии:
//   - /start <token>  — completion линковки через LinkCompleter, на успех
//     отправляется приветствие, на ошибку — «токен невалиден».
//   - /start          — generic приветствие + инструкция «привяжи через сайт».
func (b *Bot) handleStart(c tele.Context) error {
	args := c.Args()
	chatID := c.Chat().ID

	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return c.Send(welcomeNoToken, &tele.SendOptions{
			ParseMode:             tele.ModeHTML,
			DisableWebPagePreview: true,
		})
	}

	token := strings.TrimSpace(args[0])
	if b.linker == nil {
		b.logger.Warn("bot: /start with token but no linker configured",
			slog.Int64("chat_id_hash", maskChatID(chatID)))
		return c.Send(welcomeNoToken, &tele.SendOptions{
			ParseMode:             tele.ModeHTML,
			DisableWebPagePreview: true,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userID, err := b.linker.Complete(ctx, token, chatID)
	if err != nil {
		b.logger.Warn("bot: link failed",
			slog.Int64("chat_id_hash", maskChatID(chatID)),
			slog.Any("error", err),
		)
		return c.Send(linkFailedMsg, &tele.SendOptions{
			ParseMode:             tele.ModeHTML,
			DisableWebPagePreview: true,
		})
	}

	b.logger.Info("bot: link success",
		slog.String("user_id", userID),
		slog.Int64("chat_id_hash", maskChatID(chatID)),
	)
	return c.Send(linkSuccessMsg, &tele.SendOptions{
		ParseMode:             tele.ModeHTML,
		DisableWebPagePreview: true,
	})
}

func (b *Bot) handleHelp(c tele.Context) error {
	return c.Send(helpMsg, &tele.SendOptions{ParseMode: tele.ModeHTML, DisableWebPagePreview: true})
}

func (b *Bot) handleStatus(c tele.Context) error {
	return c.Send(statusMsg, &tele.SendOptions{ParseMode: tele.ModeHTML, DisableWebPagePreview: true})
}

// Start запускает long-poll/webhook цикл telebot.v3.
// Блокирующий вызов — caller обычно запускает в отдельной горутине.
// Прерывается ctx.Cancel.
func (b *Bot) Start(ctx context.Context) {
	go func() {
		<-ctx.Done()
		b.b.Stop()
	}()
	b.b.Start()
}

// SendHTML — реализация Sender, отправляет HTML-сообщение.
func (b *Bot) SendHTML(chatID int64, text string) error {
	_, err := b.b.Send(&tele.Chat{ID: chatID}, text, &tele.SendOptions{
		ParseMode:             tele.ModeHTML,
		DisableWebPagePreview: true,
	})
	if err != nil {
		return fmt.Errorf("bot: telegram send: %w", err)
	}
	return nil
}

// maskChatID возвращает усечённый ID для безопасного логирования
// (последние 4 цифры). Полный chat_id никогда не должен попадать в логи.
func maskChatID(chatID int64) int64 {
	const m = 10000
	if chatID < 0 {
		return -((-chatID) % m)
	}
	return chatID % m
}
