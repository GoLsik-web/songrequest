package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"songrequest/internal/errs"
)

// Reward — награда за баллы канала.
type Reward struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Cost      int    `json:"cost"`
	Prompt    string `json:"prompt"`
	IsEnabled bool   `json:"is_enabled"`
	// InputRequired обязателен: без текста от зрителя заказывать нечего.
	InputRequired bool `json:"is_user_input_required"`
}

// rewardPrompt — подсказка, которую зритель видит в окне награды.
const rewardPrompt = "Напиши артиста и название, ссылку на Spotify, YouTube, " +
	"Яндекс.Музыку или VK. Если трек не найдётся — баллы вернутся."

// EnsureReward создаёт награду «Заказ трека» или находит уже созданную.
//
// Награду обязано создавать само приложение, и это не прихоть: вернуть баллы
// Twitch разрешает только за награду, созданную тем же client_id. Награда,
// сделанная стримером руками, для возвратов бесполезна.
func (c *Client) EnsureReward(ctx context.Context, knownID string) (*Reward, error) {
	user := c.Account()
	if user == nil {
		return nil, errs.New(errs.TwitchAuthExpired, "Сначала подключи Twitch.")
	}
	if !user.HasChannelPoints() {
		return nil, errs.New(errs.TwitchNoAffiliate,
			"На канале нет баллов: они появляются только у аффилиатов и партнёров Twitch. Заказы за баллы работать не будут.")
	}

	cfg := c.cfg.Get()

	// Награда с прошлого запуска могла остаться — тогда просто проверяем её
	// и подгоняем название со стоимостью под настройки.
	if knownID != "" {
		existing, err := c.findReward(ctx, user.ID, knownID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return c.updateReward(ctx, user.ID, existing, cfg.RewardTitle, cfg.RewardCost)
		}
		c.log.Info("прежней награды на канале нет, создаю заново", "прежний_id", knownID)
	}

	body := map[string]any{
		"title":                                 cfg.RewardTitle,
		"cost":                                  cfg.RewardCost,
		"prompt":                                rewardPrompt,
		"is_user_input_required":                true,
		"should_redemptions_skip_request_queue": false,
		"background_color":                      "#C8F751",
	}

	var out struct {
		Data []Reward `json:"data"`
	}
	err := c.do(ctx, http.MethodPost,
		"/channel_points/custom_rewards?broadcaster_id="+url.QueryEscape(user.ID), body, &out)
	if err != nil {
		// Награда с таким названием уже есть — но создана не нами, а значит
		// баллы за неё вернуть нельзя. Об этом надо сказать прямо.
		if isDuplicateTitle(err) {
			return nil, errs.New(errs.TwitchForeignAward, fmt.Sprintf(
				"На канале уже есть награда «%s», созданная не этим приложением. Баллы за неё вернуть невозможно — переименуй её в настройках Twitch или задай другое название в настройках приложения.",
				cfg.RewardTitle))
		}
		return nil, errs.Wrap(errs.TwitchReward, "Не получилось создать награду на канале.", err)
	}
	if len(out.Data) == 0 {
		return nil, errs.New(errs.TwitchBadResponse, "Twitch не вернул созданную награду.")
	}

	reward := out.Data[0]
	c.log.Info("создал награду на канале",
		"название", reward.Title, "стоимость", reward.Cost, "id", reward.ID)
	return &reward, nil
}

// findReward ищет награду по идентификатору среди наших наград.
func (c *Client) findReward(ctx context.Context, broadcasterID, rewardID string) (*Reward, error) {
	// only_manageable_by_broadcaster=true оставляет только награды, созданные
	// этим приложением, — именно те, за которые мы можем вернуть баллы.
	path := fmt.Sprintf("/channel_points/custom_rewards?broadcaster_id=%s&id=%s&only_manageable_by_broadcaster=true",
		url.QueryEscape(broadcasterID), url.QueryEscape(rewardID))

	var out struct {
		Data []Reward `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		// Награду могли удалить руками — это не повод падать, создадим новую.
		if errs.CodeOf(err) == errs.TwitchBadResponse {
			c.log.Debug("прежняя награда не найдена", "id", rewardID)
			return nil, nil
		}
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	return &out.Data[0], nil
}

// updateReward подгоняет название и стоимость под настройки приложения.
func (c *Client) updateReward(ctx context.Context, broadcasterID string, reward *Reward,
	title string, cost int) (*Reward, error) {

	if reward.Title == title && reward.Cost == cost && reward.IsEnabled {
		return reward, nil
	}

	path := fmt.Sprintf("/channel_points/custom_rewards?broadcaster_id=%s&id=%s",
		url.QueryEscape(broadcasterID), url.QueryEscape(reward.ID))
	body := map[string]any{
		"title":      title,
		"cost":       cost,
		"is_enabled": true,
	}

	var out struct {
		Data []Reward `json:"data"`
	}
	if err := c.do(ctx, http.MethodPatch, path, body, &out); err != nil {
		return nil, errs.Wrap(errs.TwitchReward, "Не получилось обновить награду на канале.", err)
	}
	if len(out.Data) == 0 {
		return reward, nil
	}

	c.log.Info("обновил награду", "название", title, "стоимость", cost)
	return &out.Data[0], nil
}

// SetRewardPaused включает и выключает награду. Пригодится, когда стример
// ставит заказы на паузу: пусть зрители не тратят баллы впустую.
func (c *Client) SetRewardPaused(ctx context.Context, rewardID string, paused bool) error {
	user := c.Account()
	if user == nil {
		return errs.New(errs.TwitchAuthExpired, "Сначала подключи Twitch.")
	}

	path := fmt.Sprintf("/channel_points/custom_rewards?broadcaster_id=%s&id=%s",
		url.QueryEscape(user.ID), url.QueryEscape(rewardID))

	if err := c.do(ctx, http.MethodPatch, path, map[string]any{"is_paused": paused}, nil); err != nil {
		return errs.Wrap(errs.TwitchReward, "Не получилось приостановить награду.", err)
	}
	return nil
}

// RefundRedemption возвращает баллы за заказ.
//
// Возврат в Twitch устроен как отмена: редемпшен переводится в CANCELED, и
// баллы возвращаются зрителю сами. Отдельного «вернуть баллы» не существует.
func (c *Client) RefundRedemption(ctx context.Context, rewardID, redemptionID string) error {
	return c.setRedemptionStatus(ctx, rewardID, redemptionID, "CANCELED")
}

// FulfillRedemption помечает заказ выполненным — трек отыграл.
func (c *Client) FulfillRedemption(ctx context.Context, rewardID, redemptionID string) error {
	return c.setRedemptionStatus(ctx, rewardID, redemptionID, "FULFILLED")
}

func (c *Client) setRedemptionStatus(ctx context.Context, rewardID, redemptionID, status string) error {
	user := c.Account()
	if user == nil {
		return errs.New(errs.TwitchAuthExpired, "Сначала подключи Twitch.")
	}
	if rewardID == "" || redemptionID == "" {
		return errs.New(errs.TwitchRefund, "Не знаю, за какой заказ возвращать баллы.")
	}

	path := fmt.Sprintf("/channel_points/custom_rewards/redemptions?broadcaster_id=%s&reward_id=%s&id=%s",
		url.QueryEscape(user.ID), url.QueryEscape(rewardID), url.QueryEscape(redemptionID))

	if err := c.do(ctx, http.MethodPatch, path, map[string]any{"status": status}, nil); err != nil {
		what := "вернуть баллы"
		if status == "FULFILLED" {
			what = "отметить заказ выполненным"
		}
		c.log.Error("не смог "+what, "заказ", redemptionID, "ошибка", err)
		return errs.Wrap(errs.TwitchRefund, "Не получилось "+what+" — зритель остался без ответа.", err)
	}

	c.log.Info("статус заказа обновлён", "заказ", redemptionID, "статус", status)
	return nil
}

// isDuplicateTitle распознаёт отказ «награда с таким названием уже есть».
func isDuplicateTitle(err error) bool {
	var e *errs.Error
	if !errs.As(err, &e) {
		return false
	}
	return contains(e.Message, "CREATE_CUSTOM_REWARD_DUPLICATE_REWARD") ||
		contains(e.Message, "DUPLICATE_REWARD")
}
