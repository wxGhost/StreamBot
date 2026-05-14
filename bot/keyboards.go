package bot

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// --- User-side keyboards (ReplyKeyboard) ---

// mainReplyKeyboard is shown to users at all times.
func mainReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🎮 Предложить игру"),
			tgbotapi.NewKeyboardButton("💡 Предложения на стрим"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🕵️ Анонимно"),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}

// --- Streamer-side keyboards (InlineKeyboard) ---

// proposalInlineKeyboard is attached to every forwarded proposal in the streamer chat.
func proposalInlineKeyboard(proposalID int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📢 В канал", cbData("publish", proposalID)),
			tgbotapi.NewInlineKeyboardButtonData("⭐ В топ", cbData("top", proposalID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 В архив", cbData("archive", proposalID)),
			tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить", cbData("delete", proposalID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ Информация", cbData("info", proposalID)),
		),
	)
}

// channelVoteKeyboard is attached to posts in the public channel.
func channelVoteKeyboard(proposalID, likes, dislikes int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("👍 %d", likes),
				cbData("like", proposalID),
			),
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("👎 %d", dislikes),
				cbData("dislike", proposalID),
			),
		),
	)
}

// streamerReplyKeyboard is shown to the streamer at all times.
func streamerReplyKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📦 Архив"),
			tgbotapi.NewKeyboardButton("⭐ Топ"),
			tgbotapi.NewKeyboardButton("📊 Статистика"),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}
