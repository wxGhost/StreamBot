package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"streamer-bot/db"
	"streamer-bot/models"
)

const (
	maxContentRunes  = 4000
	anonCooldown     = 30 * time.Minute
	gameCooldown     = 24 * time.Hour
	pageSize         = 10
)

// Bot encapsulates all bot logic.
type Bot struct {
	api            *tgbotapi.BotAPI
	db             *db.DB
	streamerChatID int64
	channelID      int64
}

func New(token string, database *db.DB, streamerChatID, channelID int64) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("init bot api: %w", err)
	}
	return &Bot{api: api, db: database, streamerChatID: streamerChatID, channelID: channelID}, nil
}

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
	if msg.From == nil || msg.From.IsBot {
		return
	}

	// Streamer's own chat
	if msg.Chat.ID == b.streamerChatID {
		b.handleStreamerMessage(ctx, msg)
		return
	}

	userID := msg.From.ID

	// /start command
	if msg.IsCommand() && msg.Command() == "start" {
		globalState.clear(userID)
		b.sendWelcome(msg.Chat.ID)
		return
	}

	// Menu buttons
	switch msg.Text {
	case "🎮 Предложить игру":
		globalState.set(userID, stateGame)
		b.reply(msg.Chat.ID, "🎮 <b>Предложи игру!</b>\n\nНапиши название игры так, как оно звучит в оригинале. Например: <i>Elden Ring</i>, <i>Cyberpunk 2077</i>.")
		return
	case "💡 Предложения на стрим":
		globalState.set(userID, stateStream)
		b.reply(msg.Chat.ID, "💡 <b>Предложение на стрим!</b>\n\nПришли своё предложение — я обязательно передам стримеру.")
		return
	case "🕵️ Анонимно":
		globalState.set(userID, stateAnon)
		b.reply(msg.Chat.ID, "🕵️ <b>Анонимное предложение.</b>\n\nОтправляй — перешлю стримеру без указания твоего имени.")
		return
	}

	// Accept proposal if user has active state
	st := globalState.get(userID)
	if st == stateIdle {
		b.sendWelcome(msg.Chat.ID)
		return
	}
	b.acceptProposal(ctx, msg, st)
}

// handleStreamerMessage handles messages from the streamer.
func (b *Bot) handleStreamerMessage(ctx context.Context, msg *tgbotapi.Message) {
	// Check if streamer has a pending action (e.g. waiting for block duration)
	action, targetUserID, err := b.db.GetPending(ctx, b.streamerChatID)
	if err == nil && action == "block" && targetUserID != 0 {
		b.handlePendingBlock(ctx, msg, targetUserID)
		return
	}

	if msg.IsCommand() {
		switch msg.Command() {
		case "archive":
			b.sendArchive(ctx, msg.Chat.ID)
		case "top":
			b.sendTop(ctx, msg.Chat.ID)
		case "stats":
			b.sendStats(ctx, msg.Chat.ID)
		case "gamestats":
			b.sendGameStats(ctx, msg.Chat.ID)
		case "help", "start":
			b.reply(msg.Chat.ID, streamerHelp)
		}
		return
	}

	switch msg.Text {
	case "📦 Архив":
		b.sendArchive(ctx, msg.Chat.ID)
	case "⭐ Топ":
		b.sendTop(ctx, msg.Chat.ID)
	case "📊 Статистика":
		b.sendStats(ctx, msg.Chat.ID)
	case "🎮 Статистика игр":
		b.sendGameStats(ctx, msg.Chat.ID)
	}
}

// handlePendingBlock processes the streamer's reply with block duration in minutes.
func (b *Bot) handlePendingBlock(ctx context.Context, msg *tgbotapi.Message, targetUserID int64) {
	minutes, err := strconv.Atoi(strings.TrimSpace(msg.Text))
	if err != nil || minutes <= 0 {
		b.reply(msg.Chat.ID, "⚠️ Введи количество минут числом, например: <b>40</b>")
		return
	}

	until := time.Now().Add(time.Duration(minutes) * time.Minute)
	if err := b.db.BlockUser(ctx, targetUserID, until); err != nil {
		log.Printf("ERROR BlockUser uid=%d: %v", targetUserID, err)
		b.reply(msg.Chat.ID, "❌ Ошибка при блокировке.")
		return
	}

	_ = b.db.ClearPending(ctx, b.streamerChatID)
	b.reply(msg.Chat.ID, fmt.Sprintf("🚫 Пользователь заблокирован на <b>%d мин.</b> (до %s UTC).",
		minutes, until.UTC().Format("15:04 02.01")))
}

// acceptProposal validates and saves a proposal, then forwards to streamer.
func (b *Bot) acceptProposal(ctx context.Context, msg *tgbotapi.Message, st userState) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	// Check block (silent — don't tell user they are blocked)
	blocked, err := b.db.IsBlocked(ctx, userID)
	if err != nil {
		log.Printf("WARN IsBlocked uid=%d: %v", userID, err)
	}
	if blocked {
		// Silent: just clear state and do nothing
		globalState.clear(userID)
		return
	}

	content := extractContent(msg)
	if content == "" {
		b.reply(chatID, "Пожалуйста, отправь текстовое сообщение или перешли пост из канала.")
		return
	}
	if utf8.RuneCountInString(content) > maxContentRunes {
		b.reply(chatID, fmt.Sprintf("⚠️ Сообщение слишком длинное. Максимум %d символов.", maxContentRunes))
		return
	}

	// Anon cooldown
	if st == stateAnon {
		exp, err := b.db.GetCooldown(ctx, userID, "anon")
		if err != nil {
			log.Printf("WARN GetCooldown uid=%d anon: %v", userID, err)
		}
		if !exp.IsZero() {
			remaining := time.Until(exp).Round(time.Minute)
			b.reply(chatID, fmt.Sprintf("⏳ Следующее анонимное сообщение ты сможешь отправить через <b>%d мин.</b>",
				int(remaining.Minutes())+1))
			return
		}
	}

	// Game cooldown (per user per game title, 24h)
	if st == stateGame {
		normalized := db.NormalizeGameTitle(content)
		cdKey := "game:" + normalized
		exp, err := b.db.GetCooldown(ctx, userID, cdKey)
		if err != nil {
			log.Printf("WARN GetCooldown uid=%d game: %v", userID, err)
		}
		if !exp.IsZero() {
			remaining := time.Until(exp).Round(time.Minute)
			hrs := int(remaining.Hours())
			mins := int(remaining.Minutes()) % 60
			b.reply(chatID, fmt.Sprintf("⏳ Эту игру ты уже предлагал. Повторно можно через <b>%dч %dмин.</b>", hrs, mins))
			return
		}
	}

	// Build proposal
	p := &models.Proposal{Content: content}
	switch st {
	case stateGame:
		p.Type = models.TypeGame
	case stateStream:
		p.Type = models.TypeStream
	case stateAnon:
		p.Type = models.TypeAnon
	}

	if st != stateAnon {
		p.UserID = &msg.From.ID
		if msg.From.UserName != "" {
			p.Username = &msg.From.UserName
		}
		if msg.From.FirstName != "" {
			p.FirstName = &msg.From.FirstName
		}
	}

	if err := b.db.CreateProposal(ctx, p); err != nil {
		log.Printf("ERROR CreateProposal uid=%d: %v", userID, err)
		b.reply(chatID, "Что-то пошло не так 😔 Попробуй ещё раз позже.")
		return
	}

	// Set cooldowns
	if st == stateAnon {
		_ = b.db.SetCooldown(ctx, userID, "anon", time.Now().Add(anonCooldown))
	}
	if st == stateGame {
		normalized := db.NormalizeGameTitle(content)
		_ = b.db.SetCooldown(ctx, userID, "game:"+normalized, time.Now().Add(gameCooldown))

		// Update game stack
		proposer := db.GameProposer{UserID: p.UserID, Username: p.Username, FirstName: p.FirstName}
		if _, err := b.db.UpsertGameStack(ctx, content, proposer); err != nil {
			log.Printf("WARN UpsertGameStack: %v", err)
		}
	}

	globalState.clear(userID)
	b.reply(chatID, "✅ Готово! Твоё предложение отправлено стримеру.")
	b.notifyStreamer(ctx, p)
}

// notifyStreamer sends the proposal to the streamer chat.
func (b *Bot) notifyStreamer(ctx context.Context, p *models.Proposal) {
	text := buildStreamerMessage(p)
	msg := tgbotapi.NewMessage(b.streamerChatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = proposalInlineKeyboard(p.ID, p.UserID)

	sent, err := b.api.Send(msg)
	if err != nil {
		log.Printf("ERROR notifyStreamer pid=%d: %v", p.ID, err)
		return
	}
	if err := b.db.SetMessageID(ctx, p.ID, int64(sent.MessageID)); err != nil {
		log.Printf("WARN SetMessageID pid=%d: %v", p.ID, err)
	}
}

// ─── Callback handler ─────────────────────────────────────────────────────────

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	log.Printf("INFO callback data=%q", cb.Data)

	if strings.HasPrefix(cb.Data, "page_") && cb.Message != nil {
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		b.handlePageCallback(ctx, cb)
		return
	}

	action, proposalID, err := parseCB(cb.Data)
	if err != nil {
		log.Printf("WARN parseCB data=%q: %v", cb.Data, err)
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		return
	}

	if action == "like" || action == "dislike" {
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		b.handleVote(ctx, cb, proposalID, action)
		return
	}

	if cb.Message == nil || cb.Message.Chat.ID != b.streamerChatID {
		_, _ = b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		return
	}

	if action == "info" {
		b.handleInfo(ctx, cb, proposalID)
		return
	}

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
	case "block":
		b.handleBlockInit(ctx, cb, proposalID)
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
		log.Printf("ERROR UpsertVote pid=%d: %v", proposalID, err)
		return
	}
	if cb.Message != nil {
		edit := tgbotapi.NewEditMessageReplyMarkup(
			cb.Message.Chat.ID, cb.Message.MessageID,
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
	post := tgbotapi.NewMessage(b.channelID, buildChannelMessage(p))
	post.ParseMode = tgbotapi.ModeHTML
	post.ReplyMarkup = channelVoteKeyboard(proposalID, 0, 0)
	if _, err := b.api.Send(post); err != nil {
		log.Printf("ERROR publish pid=%d: %v", proposalID, err)
		return
	}
	b.editStreamerMsg(cb.Message, p, "✅ Опубликовано в канале")
}

func (b *Bot) handleSetStatus(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int, status models.ProposalStatus, note string) {
	if err := b.db.SetStatus(ctx, proposalID, status); err != nil {
		log.Printf("ERROR SetStatus pid=%d: %v", proposalID, err)
		return
	}
	if p, err := b.db.GetProposal(ctx, proposalID); err == nil {
		b.editStreamerMsg(cb.Message, p, note)
	}
}

func (b *Bot) handleDelete(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int) {
	p, err := b.db.GetProposal(ctx, proposalID)
	if err == nil && p.MessageID != nil {
		_, _ = b.api.Request(tgbotapi.NewDeleteMessage(b.streamerChatID, int(*p.MessageID)))
	}
	if err := b.db.DeleteProposal(ctx, proposalID); err != nil {
		log.Printf("ERROR DeleteProposal pid=%d: %v", proposalID, err)
	}
}

func (b *Bot) handleInfo(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int) {
	p, err := b.db.GetProposal(ctx, proposalID)
	if err != nil {
		log.Printf("ERROR GetProposal pid=%d: %v", proposalID, err)
		_, _ = b.api.Request(tgbotapi.NewCallbackWithAlert(cb.ID, "❌ Ошибка получения данных"))
		return
	}
	total, _ := b.db.CountProposals(ctx)
	text := buildInfoMessage(p, total)
	runes := []rune(text)
	if len(runes) > 200 {
		text = string(runes[:197]) + "..."
	}
	_, _ = b.api.Request(tgbotapi.NewCallbackWithAlert(cb.ID, text))
}

// handleBlockInit starts the block flow: save pending state and ask streamer for duration.
func (b *Bot) handleBlockInit(ctx context.Context, cb *tgbotapi.CallbackQuery, proposalID int) {
	p, err := b.db.GetProposal(ctx, proposalID)
	if err != nil || p.UserID == nil {
		b.reply(b.streamerChatID, "❌ Нельзя заблокировать анонимного пользователя.")
		return
	}
	if err := b.db.SetPending(ctx, b.streamerChatID, "block", *p.UserID); err != nil {
		log.Printf("ERROR SetPending: %v", err)
		return
	}
	name := html.EscapeString(p.DisplayName())
	b.reply(b.streamerChatID, fmt.Sprintf(
		"🚫 Блокировка пользователя <b>%s</b>\n\nНа сколько минут заблокировать? Напиши число:", name))
}

func (b *Bot) handlePageCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if cb.Message.Chat.ID != b.streamerChatID {
		return
	}
	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		return
	}
	offset := 0
	fmt.Sscanf(parts[1], "%d", &offset)
	if offset < 0 {
		offset = 0
	}
	del := tgbotapi.NewDeleteMessage(cb.Message.Chat.ID, cb.Message.MessageID)
	_, _ = b.api.Request(del)
	switch parts[0] {
	case "page_archive":
		b.sendPagedList(ctx, cb.Message.Chat.ID, models.StatusArchived, "📦 Архив", offset)
	case "page_top":
		b.sendPagedTop(ctx, cb.Message.Chat.ID, offset)
	}
}

// ─── Streamer list commands ───────────────────────────────────────────────────

func (b *Bot) sendArchive(ctx context.Context, chatID int64) {
	b.sendPagedList(ctx, chatID, models.StatusArchived, "📦 Архив", 0)
}

func (b *Bot) sendTop(ctx context.Context, chatID int64) {
	b.sendPagedTop(ctx, chatID, 0)
}

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
	msg.ReplyMarkup = paginationKeyboard("page_archive", offset, hasMore)
	_, _ = b.api.Send(msg)
}

func (b *Bot) sendPagedTop(ctx context.Context, chatID int64, offset int) {
	all, err := b.db.ListTop(ctx, pageSize+1+offset)
	if err != nil {
		b.reply(chatID, "❌ Ошибка при получении топа.")
		return
	}
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
	msg.ReplyMarkup = paginationKeyboard("page_top", offset, hasMore)
	_, _ = b.api.Send(msg)
}

func (b *Bot) sendStats(ctx context.Context, chatID int64) {
	total, _ := b.db.CountProposals(ctx)
	active, _ := b.db.CountByStatus(ctx, models.StatusActive)
	archived, _ := b.db.CountByStatus(ctx, models.StatusArchived)
	top, _ := b.db.CountByStatus(ctx, models.StatusTop)
	b.reply(chatID, fmt.Sprintf(
		"📊 <b>Статистика</b>\n\nВсего: <b>%d</b>\nАктивных: <b>%d</b>\nВ архиве: <b>%d</b>\nВ топе: <b>%d</b>",
		total, active, archived, top,
	))
}

func (b *Bot) sendGameStats(ctx context.Context, chatID int64) {
	stacks, err := b.db.GetTopGameStacks(ctx, 20)
	if err != nil || len(stacks) == 0 {
		b.reply(chatID, "🎮 Статистика игр пуста.")
		return
	}
	var sb strings.Builder
	sb.WriteString("<b>🎮 Топ запрошенных игр</b>\n\n")
	for i, s := range stacks {
		sb.WriteString(fmt.Sprintf("%d. <b>%s</b> — %d раз\n",
			i+1, html.EscapeString(s.GameTitleOrig), s.Count))
		// Show up to 3 proposers
		shown := s.Proposers
		if len(shown) > 3 {
			shown = shown[len(shown)-3:]
		}
		for _, pr := range shown {
			name := "Аноним"
			if pr.FirstName != nil {
				name = *pr.FirstName
			} else if pr.Username != nil {
				name = "@" + *pr.Username
			}
			sb.WriteString(fmt.Sprintf("   • %s\n", html.EscapeString(name)))
		}
		if len(s.Proposers) > 3 {
			sb.WriteString(fmt.Sprintf("   <i>...и ещё %d</i>\n", len(s.Proposers)-3))
		}
		sb.WriteString("\n")
	}
	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeHTML
	_, _ = b.api.Send(msg)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (b *Bot) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	if chatID == b.streamerChatID {
		msg.ReplyMarkup = streamerReplyKeyboard()
	} else {
		msg.ReplyMarkup = mainReplyKeyboard()
	}
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("WARN reply chatID=%d: %v", chatID, err)
	}
}

func (b *Bot) sendWelcome(chatID int64) {
	// Welcome message with rules + inline link buttons + sets reply keyboard
	msg := tgbotapi.NewMessage(chatID, welcomeText)
	msg.ParseMode = tgbotapi.ModeHTML
	// Use combined markup: reply keyboard is set separately right after
	msg.ReplyMarkup = mainReplyKeyboard()
	_, _ = b.api.Send(msg)

	// Follow up with inline link buttons as a second message
	links := tgbotapi.NewMessage(chatID, "🔗 Ссылки стримера:")
	links.ParseMode = tgbotapi.ModeHTML
	links.ReplyMarkup = userInlineLinks()
	_, _ = b.api.Send(links)
}

// sendLinksOnly re-sends inline links when user taps a link button.
func (b *Bot) sendLinksOnly(chatID int64) {
	links := tgbotapi.NewMessage(chatID, "🔗 Ссылки стримера:")
	links.ParseMode = tgbotapi.ModeHTML
	links.ReplyMarkup = userInlineLinks()
	_, _ = b.api.Send(links)
}

func (b *Bot) editStreamerMsg(orig *tgbotapi.Message, p *models.Proposal, note string) {
	if orig == nil {
		return
	}
	text := buildStreamerMessage(p) + "\n\n<i>" + html.EscapeString(note) + "</i>"
	edit := tgbotapi.NewEditMessageText(orig.Chat.ID, orig.MessageID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	kb := proposalInlineKeyboard(p.ID, p.UserID)
	edit.ReplyMarkup = &kb
	_, _ = b.api.Send(edit)
}

func extractContent(msg *tgbotapi.Message) string {
	if msg.Text != "" {
		return msg.Text
	}
	if msg.Caption != "" {
		return msg.Caption
	}
	return ""
}

func paginationKeyboard(prefix string, offset int, hasMore bool) *tgbotapi.InlineKeyboardMarkup {
	var nav []tgbotapi.InlineKeyboardButton
	if offset > 0 {
		prev := offset - pageSize
		if prev < 0 {
			prev = 0
		}
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", fmt.Sprintf("%s:%d", prefix, prev)))
	}
	if hasMore {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("▶️ Дальше", fmt.Sprintf("%s:%d", prefix, offset+pageSize)))
	}
	if len(nav) == 0 {
		return nil
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(nav...))
	return &kb
}

// ─── Message templates ────────────────────────────────────────────────────────

const welcomeText = `👋 <b>Привет!</b>

Я помогаю передавать предложения стримеру.

📌 <b>Правила:</b>
• Пиши название игры <b>так, как оно звучит в оригинале</b> (например: <i>Elden Ring</i>, <i>The Witcher 3</i>)
• Не отправляй бессмысленные сообщения и спам
• За спам или нарушение правил последует <b>временная блокировка</b>

Нажми кнопку ниже, чтобы выбрать тип предложения 👇`

const streamerHelp = `<b>Команды стримера:</b>

/archive — архив предложений
/top — топ предложений
/stats — общая статистика
/gamestats — статистика запрошенных игр
/help — эта справка`

func buildStreamerMessage(p *models.Proposal) string {
	var sb strings.Builder
	sb.WriteString(p.TypeTag() + " ")
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
	return p.TypeTag() + "\n\n" + html.EscapeString(p.Content)
}

func buildInfoMessage(p *models.Proposal, totalCount int) string {
	var sb strings.Builder
	sb.WriteString(p.TypeTag() + "\n")
	if p.UserID != nil {
		link := p.ProfileLink()
		if link != "" {
			sb.WriteString("👤 " + link + "\n")
		} else {
			sb.WriteString(fmt.Sprintf("👤 ID: %d\n", *p.UserID))
		}
	} else {
		sb.WriteString("👤 Анонимно\n")
	}
	sb.WriteString(fmt.Sprintf("🕐 %s\n", p.CreatedAt.UTC().Format("02.01.2006 15:04 UTC")))
	sb.WriteString(fmt.Sprintf("🔢 #%d из %d\n", p.ID, totalCount))
	sb.WriteString(fmt.Sprintf("👍 %d  👎 %d", p.Likes, p.Dislikes))
	return sb.String()
}

func buildListMessage(title string, proposals []*models.Proposal, offset int) string {
	var sb strings.Builder
	sb.WriteString("<b>" + html.EscapeString(title) + "</b>\n\n")
	for i, p := range proposals {
		preview := []rune(p.Content)
		if len(preview) > 80 {
			preview = append(preview[:80], '…')
		}
		score := ""
		if p.Likes > 0 || p.Dislikes > 0 {
			score = fmt.Sprintf(" 👍%d 👎%d", p.Likes, p.Dislikes)
		}
		sb.WriteString(fmt.Sprintf("%d. %s <b>%s</b>%s\n%s\n\n",
			offset+i+1, p.TypeTag(),
			html.EscapeString(p.DisplayName()), score,
			html.EscapeString(string(preview)),
		))
	}
	return sb.String()
}