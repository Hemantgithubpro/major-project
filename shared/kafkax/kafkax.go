package kafkax

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Envelope struct {
	EventID       string         `json:"event_id"`
	EventType     string         `json:"event_type"`
	OccurredAt    string         `json:"occurred_at"`
	TransactionID string         `json:"transaction_id"`
	AggregateID   string         `json:"aggregate_id"`
	CorrelationID string         `json:"correlation_id"`
	Source        string         `json:"source"`
	Region        string         `json:"region"`
	Version       int            `json:"version"`
	Payload       map[string]any `json:"payload"`
}

func New(source, typ, tx, agg, corr string, payload map[string]any) Envelope {
	if tx == "" {
		tx = uuid.NewString()
	}
	return Envelope{
		EventID: uuid.NewString(), EventType: typ,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TransactionID: tx, AggregateID: agg, CorrelationID: corr,
		Source: source, Region: "region-a", Version: 1, Payload: payload,
	}
}

func Publish(broker, topic string, e Envelope) error {
	w := &kafka.Writer{Addr: kafka.TCP(broker), Topic: topic, Balancer: &kafka.Hash{}}
	defer w.Close()
	b, _ := json.Marshal(e)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return w.WriteMessages(ctx, kafka.Message{Key: []byte(e.AggregateID), Value: b})
}
