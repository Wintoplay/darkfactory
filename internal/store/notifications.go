package store

import (
	"errors"
	"fmt"
)

// NotificationSettings is the owner's alert policy. It deliberately separates
// "what happened" (the activity ring keeps every event) from "what may make
// noise" (a desktop bubble or a phone notification), so turning off replies
// does not hide them from the bell list.
//
// ChildSessions controls OpenCode child/subagent chats. Those are the bulk of
// a busy parent's traffic and are usually not something the owner must see.
type NotificationSettings struct {
	FinishedSessions bool `json:"finishedSessions"`
	Replies          bool `json:"replies"`
	BlockedRuns      bool `json:"blockedRuns"`
	Prompts          bool `json:"prompts"`
	ChildSessions    bool `json:"childSessions"`
}

// DefaultNotificationSettings is quiet by default: a finished PTY session,
// a blocked run, a permission request or a question may alert; every answer
// and every subagent chat stays in the list without ringing.
func DefaultNotificationSettings() NotificationSettings {
	return NotificationSettings{
		FinishedSessions: true,
		Replies:          false,
		BlockedRuns:      true,
		Prompts:          true,
		ChildSessions:    false,
	}
}

func (s *Store) GetNotificationSettings() (NotificationSettings, error) {
	settings := DefaultNotificationSettings()
	err := s.get(settingsBucket, notificationSettingsKey, &settings)
	if errors.Is(err, ErrNotFound) {
		return DefaultNotificationSettings(), nil
	}
	return settings, err
}

func (s *Store) PutNotificationSettings(settings NotificationSettings) error {
	if s == nil || s.db == nil {
		return errors.New("store is not open")
	}
	if err := s.put(settingsBucket, notificationSettingsKey, settings); err != nil {
		return fmt.Errorf("save notification settings: %w", err)
	}
	return nil
}
