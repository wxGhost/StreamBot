package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"streamer-bot/db"
	"streamer-bot/models"
)

const (
	maxContentLength = 4000 // chars
	maxContentRunes  = 4000
)

// Bot encapsulates all bot logic.
type Bot struct {
	api           *tgbotapi.BotAPI
	db            *db.DB
	streamerChatID int64
	channelID      int64
}

// New creates a new Bot instance.
func New(token string, database *db.DB, streamerChatID, channelID int64) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("init bot api: %w", err)
	}
	return &Bot{
		api:           api,
		db:            database,
		streamerChatID: streamerChatID,
		channelID:      channelID,
	}, nil
}

// API returns the underlying BotAPI (used to register webhook).
func (b *Bot) API() *tgbotapi.BotAPI { return b.api }

// HandleUpdate is the main dispatcher.
func (b *Bot) HandleUpdate(ctx context.Context, update tgbotapi.Update) {
	switch {
	case update.CallbackQuery != nil:
		b.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil:
		b.handleMessage(ctx, update.Message)
	}
}

// ─── Message handler ──────────────────────────────────────────────────────────

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	// Only allow messages from non-bot users
	if msg.From == nil || msg.From.IsBot {
		return
	}

	userID := msg.From.ID

	// Streamer's own commands (only from streamer chat)
	if msg.Chat.ID == b.streamerChatID {
		b.handleStreamerCommand(ctx, msg)
		return
	}

	// User commands / state machine
	// Handle /start command (works as "/start@botname" too)
	if msg.IsCommand() && msg.Command() == "start" {
		b.sendWelcome(msg.Chat.ID)
		globalState.clear(userID)
		return
	}

	switch msg.Text {
	case "🎮 Предложить игру":
		globalState.set(userID, stateGame)
		b.reply(msg.Chat.ID, "🎮 <b>Предложи игру!</b>\n\nОтправь мне название или описание игры — я обязательно передам стримеру. Можешь написать текст или переслать сообщение.")
		return
	case "💡 Предложения на стрим":
		globalState.set(userID, stateStream)
		b.reply(msg.Chat.ID, "💡 <b>Предложение на стрим!</b>\n\nПришли своё предложение — я обязательно передам стримеру. Можешь написать текст или переслать сообщение.")
		return
	case "🕵️ Анонимно":
		globalState.set(userID, stateAnon)
		b.reply(msg.Chat.ID, "🕵️ <b>Анонимное предложение.</b>\n\nОтправляй — я перешлю его стримеру без указания твоего имени.")
		return
	}

	// If user has an active state — accept their proposal
	st := globalState.get(userID)
	if st == stateIdle {
		b.sendWelcome(msg.Chat.ID)
		return
	}

	b.acceptProposal(ctx, msg, st)
}

// handleStreamerCommand processes commands typed by the streamer in their own chat.
// Uses msg.Command() so that "/archive@botname" also works in group/channel chats.
func (b *Bot) handleStreamerCommand(ctx context.Context, msg *tgbotapi.Message) {
	if !msg.IsCommand() {
		// Streamer typed plain text — ignore silently (never create proposals from streamer side).
		return
	}
	switch msg.Command() {
	case "archive":
		b.sendArchive(ctx, msg.Chat.ID)
	case "top":
		b.sendTop(ctx, msg.Chat.ID)
	case "stats":
		b.sendStats(ctx, msg.Chat.ID)
	case "help", "start":
		b.reply(msg.Chat.ID, streamerHelp)
	}
}

// acceptProposal validates and saves a proposal, then forwards it to the streamer.
func (b *Bot) acceptProposal(ctx context.Context, msg *tgbotapi.Message, st userState) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	// Determine content: prefer ForwardOrigin caption/text, else raw text
	content := extractContent(msg)
	if content == "" {
		b.reply(chatID, "Пожалуйста, отправь текстовое сообщение или перешли пост из канала.")
		return
	}

	// Validate length
	if utf8.RuneCountInString(content) > maxContentRunes {
		b.reply(chatID, fmt.Sprintf("⚠️ Сообщение слишком длинное. Максимум %d символов.", maxContentRunes))
		return
	}

	// Build proposal
	p := &models.Proposal{
		Content: content,
	}

	switch st {
	case stateGame:
		p.Type = models.TypeGame
	case stateStream:
		p.Type = models.TypeStream
	case stateAnon:
		p.Type = models.TypeAnon
	}

	// Attach user identity only for non-anon
	if st != stateAnon {
		p.UserID = &msg.From.ID
		if msg.From.UserName != "" {
			p.Username = &msg.From.UserName
		}
		if msg.From.FirstName != "" {
			p.FirstName = &msg.From.FirstName
		}
	}

	// Save to DB
	if err := b.db.CreateProposal(ctx, p); err != nil {
		log.Printf("ERROR CreateProposal uid=%d: %v", userID, err)
		b.reply(chatID, "Что-то пошло не так 😔 Попробуй ещё раз позже.")
		return
	}

	// Confirm to user
	globalState.clear(userID)
	b.reply(chatID, "✅ Готово! Твоё предложение отправлено стримеру.")

	// Forward to streamer
	b.notifyStreamer(ctx, p)
}

// notifyStreamer sends the proposal to the streamer chat with inline action buttons.
func (b *Bot) notifyStreamer(ctx context.Context, p *models.Proposal) {
	text := buildStreamerMessage(p)
	msg := tgbotapi.NewMessage(b.streamerChatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = proposalInlineKeyboard(p.ID)

	sent, err := b.api.Send(msg)
	if err != nil {
		log.Printf("ERROR notifyStreamer pid=%d: %v", p.ID, err)
		return
	}

	// Store message ID for later deletion/editing
	if err := b.db.SetMessageID(ctx, p.ID, int64(sent.MessageID)); err != nil {
		log.Printf("WARN SetMessageID pid=%d: %v", p.ID, err)
	}
}

// ─── Callback handler ─────────────────────────────────────────────────────────

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	log.Printf("INFO callback: id=%s data=%q chatID=%d", cb.ID, cb.Data, func() int64 {
		if cb.Message != nil { return cb.Message.Chat.ID }
		return 0
	}())

	// Pagination callbacks have format "page_archive:N" or "page_top:N"
	if strings.HasPrefix(cb.Data, "page_") && cb.Message != nil {
		// Answer silently then paginate
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		b.handlePageCallback(ctx, cb)
		return
	}

	action, proposalID, err := parseCB(cb.Data)
	if err != nil {
		log.Printf("WARN parseCB data=%q err=%v", cb.Data, err)
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		return
	}
	log.Printf("INFO callback action=%s proposalID=%d streamerChatID=%d msgChatID=%d",
		action, proposalID, b.streamerChatID, func() int64 {
			if cb.Message != nil { return cb.Message.Chat.ID }
			return 0
		}())

	// Vote callbacks can come from the public channel (any user)
	if action == "like" || action == "dislike" {
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		b.handleVote(ctx, cb, proposalID, action)
		return
	}

	// All other actions are streamer-only
	if cb.Message == nil || cb.Message.Chat.ID != b.streamerChatID {
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		return
	}

	// "info" answers with an alert — don't pre-answer
	if action == "info" {
		b.handleInfo(ctx, cb, proposalID)
		return
	}

	// All other actions: answer silently first
	_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))

	switch action {
	case "publish":
		b.handlePublish(ctx, cb, proposalID)
	case "top":
		b.handleSetStatus(ctx, cb, proposalID, models.StatusTop, "⭐ Добавлено в топ!")
	case "archive":
		b.handleSetStatus(ctx, cb, proposalID, models.StatusArchived, "📦 Отложено в архив.")
	case "delete":
		b.handleDelete(ctx, cb, proposalID)
	}
}

func (b *Bot) handlePageCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	// Only streamer can paginate
	if cb.Message.Chat.ID != b.streamerChatID {
		return
	}
	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		return
	}
	pageType := parts[0]    // "page_archive" or "page_top"
	offset := 0
	fmt.Sscanf(parts[1], "%d", &offset)
	if offset < 0 {
		offset = 0
	}

	// Delete the old list message and send a new one
	del := tgbotapi.NewDeleteMessage(cb.Message.Chat.ID, cb.Message.MessageID)
	_, _ = b.api.Request(del)

	switch pageType {
	case "page_archive":
		b.sendPagedList(ctx, cb.Message.Chat.ID, models.StatusArchived, "📦 Архив", offset)
	case "page_top":
		b.sendPagedTop(ctx, cb.Message.Chat.ID, offset)
	}
}

func (b *Bot) handleVote(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int, action string) {
	if cb.From == nil {
		return
	}
	value := 1
	if action == "dislike" {
		value = -1
	}

	likes, dislikes, err := b.db.UpsertVote(ctx, proposalID, cb.From.ID, value)
	if err != nil {
		log.Printf("ERROR UpsertVote pid=%d uid=%d: %v", proposalID, cb.From.ID, err)
		return
	}

	// Update vote counter on the channel message
	if cb.Message != nil {
		edit := tgbotapi.NewEditMessageReplyMarkup(
			cb.Message.Chat.ID,
			cb.Message.MessageID,
			channelVoteKeyboard(proposalID, likes, dislikes),
		)
		_, _ = b.api.Send(edit)
	}
}

func (b *Bot) handlePublish(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int) {
	p, err := b.db.GetProposal(ctx, proposalID)
	if err != nil {
		log.Printf("ERROR GetProposal pid=%d: %v", proposalID, err)
		return
	}

	// Post to channel
	text := buildChannelMessage(p)
	post := tgbotapi.NewMessage(b.channelID, text)
	post.ParseMode = tgbotapi.ModeHTML
	post.ReplyMarkup = channelVoteKeyboard(proposalID, 0, 0)

	_, err = b.api.Send(post)
	if err != nil {
		log.Printf("ERROR publish to channel pid=%d: %v", proposalID, err)
		notif := tgbotapi.NewCallback(cb.ID, "❌ Ошибка публикации")
		_, _ = b.api.Request(notif)
		return
	}

	// Edit original streamer message to reflect published state
	b.editStreamerMsg(cb.Message, p, "✅ Опубликовано в канале")
}

func (b *Bot) handleSetStatus(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int, status models.ProposalStatus, note string) {
	if err := b.db.SetStatus(ctx, proposalID, status); err != nil {
		log.Printf("ERROR SetStatus pid=%d status=%s: %v", proposalID, status, err)
		return
	}
	p, _ := b.db.GetProposal(ctx, proposalID)
	if p != nil {
		b.editStreamerMsg(cb.Message, p, note)
	}
}

func (b *Bot) handleDelete(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int) {
	p, err := b.db.GetProposal(ctx, proposalID)
	if err == nil && p.MessageID != nil {
		del := tgbotapi.NewDeleteMessage(b.streamerChatID, int(*p.MessageID))
		_, _ = b.api.Request(del)
	}
	if err := b.db.DeleteProposal(ctx, proposalID); err != nil {
		log.Printf("ERROR DeleteProposal pid=%d: %v", proposalID, err)
	}
}

func (b *Bot) handleInfo(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int) {
	p, err := b.db.GetProposal(ctx, proposalID)
	if err != nil {
		log.Printf("ERROR GetProposal pid=%d: %v", proposalID, err)
		answer := tgbotapi.NewCallbackWithAlert(cb.ID, "❌ Ошибка получения данных")
		_, _ = b.api.Request(answer)
		return
	}
	total, _ := b.db.CountProposals(ctx)
	text := buildInfoMessage(p, total)

	// Telegram limits alert text to 200 chars
	runes := []rune(text)
	if len(runes) > 200 {
		runes = runes[:197]
		text = string(runes) + "..."
	}

	// NewCallbackWithAlert sets ShowAlert=true automatically
	answer := tgbotapi.NewCallbackWithAlert(cb.ID, text)
	_, _ = b.api.Request(answer)
}

// ─── Streamer list commands ───────────────────────────────────────────────────

const pageSize = 10

func (b *Bot) sendArchive(ctx context.Context, chatID int64) {
	b.sendPagedList(ctx, chatID, models.StatusArchived, "📦 Архив", 0)
}

func (b *Bot) sendTop(ctx context.Context, chatID int64) {
	b.sendPagedTop(ctx, chatID, 0)
}

// sendPagedList sends a paginated list of proposals with a given status.
func (b *Bot) sendPagedList(ctx context.Context, chatID int64, status models.ProposalStatus, title string, offset int) {
	proposals, err := b.db.ListProposals(ctx, status, pageSize+1, offset)
	if err != nil {
		b.reply(chatID, "❌ Ошибка при получении списка.")
		return
	}
	if len(proposals) == 0 && offset == 0 {
		b.reply(chatID, fmt.Sprintf("%s пуст.", title))
		return
	}

	hasMore := len(proposals) > pageSize
	if hasMore {
		proposals = proposals[:pageSize]
	}

	text := buildListMessage(title, proposals, offset)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	// Pagination buttons
	var rows [][]tgbotapi.InlineKeyboardButton
	var nav []tgbotapi.InlineKeyboardButton
	if offset > 0 {
		prev := offset - pageSize
		if prev < 0 {
			prev = 0
		}
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", fmt.Sprintf("page_archive:%d", prev)))
	}
	if hasMore {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("▶️ Дальше", fmt.Sprintf("page_archive:%d", offset+pageSize)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}

	if _, err := b.api.Send(msg); err != nil {
		log.Printf("WARN sendPagedList: %v", err)
	}
}

// sendPagedTop sends a paginated top list.
func (b *Bot) sendPagedTop(ctx context.Context, chatID int64, offset int) {
	all, err := b.db.ListTop(ctx, pageSize+1+offset)
	if err != nil {
		b.reply(chatID, "❌ Ошибка при получении топа.")
		return
	}
	// Manual slice for offset
	if offset > len(all) {
		b.reply(chatID, "⭐ Больше нет.")
		return
	}
	all = all[offset:]
	hasMore := len(all) > pageSize
	if hasMore {
		all = all[:pageSize]
	}
	if len(all) == 0 && offset == 0 {
		b.reply(chatID, "⭐ Топ пуст.")
		return
	}

	text := buildListMessage("⭐ Топ предложений", all, offset)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	var rows [][]tgbotapi.InlineKeyboardButton
	var nav []tgbotapi.InlineKeyboardButton
	if offset > 0 {
		prev := offset - pageSize
		if prev < 0 {
			prev = 0
		}
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", fmt.Sprintf("page_top:%d", prev)))
	}
	if hasMore {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("▶️ Дальше", fmt.Sprintf("page_top:%d", offset+pageSize)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}

	if _, err := b.api.Send(msg); err != nil {
		log.Printf("WARN sendPagedTop: %v", err)
	}
}

func (b *Bot) sendStats(ctx context.Context, chatID int64) {
	total, _ := b.db.CountProposals(ctx)
	active, _ := b.db.CountByStatus(ctx, models.StatusActive)
	archived, _ := b.db.CountByStatus(ctx, models.StatusArchived)
	top, _ := b.db.CountByStatus(ctx, models.StatusTop)

	b.reply(chatID, fmt.Sprintf(
		"📊 <b>Статистика</b>\n\nВсего предложений: <b>%d</b>\nАктивных: <b>%d</b>\nВ архиве: <b>%d</b>\nВ топе: <b>%d</b>",
		total, active, archived, top,
	))
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (b *Bot) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	if chatID != b.streamerChatID {
		msg.ReplyMarkup = mainReplyKeyboard()
	}
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("WARN reply chatID=%d: %v", chatID, err)
	}
}

func (b *Bot) sendWelcome(chatID int64) {
	b.reply(chatID, welcomeText)
}

func (b *Bot) editStreamerMsg(orig *tgbotapi.Message, p *models.Proposal, note string) {
	if orig == nil {
		return
	}
	text := buildStreamerMessage(p) + "\n\n<i>" + html.EscapeString(note) + "</i>"
	edit := tgbotapi.NewEditMessageText(orig.Chat.ID, orig.MessageID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	edit.ReplyMarkup = func() *tgbotapi.InlineKeyboardMarkup {
		kb := proposalInlineKeyboard(p.ID)
		return &kb
	}()
	_, _ = b.api.Send(edit)
}

// extractContent pulls text from a message (direct text or forwarded).
func extractContent(msg *tgbotapi.Message) string {
	if msg.Text != "" {
		return msg.Text
	}
	if msg.Caption != "" {
		return msg.Caption
	}
	return ""
}

func countOrZero[T any](s []T) int { return len(s) }

// ─── Message templates ────────────────────────────────────────────────────────

const welcomeText = `👋 <b>Привет!</b>

Я помогаю передавать предложения стримеру.

Нажми кнопку ниже, чтобы выбрать тип предложения:`

const streamerHelp = `<b>Команды стримера:</b>

/archive — просмотр архива
/top — топ предложений
/stats — статистика
/help — эта справка`

func buildStreamerMessage(p *models.Proposal) string {
	var sb strings.Builder
	sb.WriteString(p.TypeTag() + " ")

	// Author line
	if p.Type == models.TypeAnon {
		sb.WriteString("• <b>Анонимно</b>\n\n")
	} else {
		name := html.EscapeString(p.DisplayName())
		link := p.ProfileLink()
		if link != "" {
			sb.WriteString(fmt.Sprintf("• <a href=%q>%s</a>\n\n", link, name))
		} else {
			sb.WriteString("• " + name + "\n\n")
		}
	}

	sb.WriteString(html.EscapeString(p.Content))
	return sb.String()
}

func buildChannelMessage(p *models.Proposal) string {
	var sb strings.Builder
	sb.WriteString(p.TypeTag() + "\n\n")
	sb.WriteString(html.EscapeString(p.Content))
	return sb.String()
}

func buildInfoMessage(p *models.Proposal, totalCount int) string {
	var sb strings.Builder
	sb.WriteString(p.TypeTag() + "\n")

	if p.UserID != nil {
		link := p.ProfileLink()
		if link != "" {
			sb.WriteString(fmt.Sprintf("👤 %s\n", link))
		} else {
			sb.WriteString(fmt.Sprintf("👤 ID: %d\n", *p.UserID))
		}
	} else {
		sb.WriteString("👤 Анонимно\n")
	}

	sb.WriteString(fmt.Sprintf("🕐 %s\n", p.CreatedAt.In(time.UTC).Format("02.01.2006 15:04 UTC")))
	sb.WriteString(fmt.Sprintf("🔢 Предложение #%d из %d\n", p.ID, totalCount))
	sb.WriteString(fmt.Sprintf("👍 %d  👎 %d", p.Likes, p.Dislikes))
	return sb.String()
}

func buildListMessage(title string, proposals []*models.Proposal, offset int) string {
	var sb strings.Builder
	sb.WriteString("<b>" + html.EscapeString(title) + "</b>\n\n")
	for i, p := range proposals {
		preview := []rune(p.Content)
		if len(preview) > 80 {
			preview = append(preview[:80], []rune("\u2026")...)
		}
		score := ""
		if p.Likes > 0 || p.Dislikes > 0 {
			score = fmt.Sprintf(" \U0001F44D%d \U0001F44E%d", p.Likes, p.Dislikes)
		}
		sb.WriteString(fmt.Sprintf(
			"%d. %s <b>%s</b>%s\n%s\n\n",
			offset+i+1,
			p.TypeTag(),
			html.EscapeString(p.DisplayName()),
			score,
			html.EscapeString(string(preview)),
		))
	}
	return sb.String()
}
