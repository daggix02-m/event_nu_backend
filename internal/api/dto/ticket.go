package dto

import "time"

// TicketTypeRequest is the create payload for a ticket tier (POST
// /events/{id}/ticket-types). Price is expressed in the currency's minor unit.
type TicketTypeRequest struct {
	Name          string     `json:"name"`
	Description   *string    `json:"description"`
	PriceMinor    int64      `json:"price_minor"`
	Currency      string     `json:"currency"`
	QuantityTotal *int       `json:"quantity_total"`
	SalesStart    *time.Time `json:"sales_start"`
	SalesEnd      *time.Time `json:"sales_end"`
}

// TicketTypeUpdate is a full-snapshot PATCH: every field carries the value to
// persist (null clears nullable fields — there is no "keep" sentinel).
type TicketTypeUpdate struct {
	Name          string     `json:"name"`
	Description   *string    `json:"description"`
	PriceMinor    int64      `json:"price_minor"`
	QuantityTotal *int       `json:"quantity_total"`
	SalesStart    *time.Time `json:"sales_start"`
	SalesEnd      *time.Time `json:"sales_end"`
	IsActive      bool       `json:"is_active"`
}

// TicketTypeDTO is the wire shape of a ticket tier.
type TicketTypeDTO struct {
	ID            string     `json:"id"`
	EventID       string     `json:"event_id"`
	Name          string     `json:"name"`
	Description   *string    `json:"description"`
	PriceMinor    int64      `json:"price_minor"`
	Currency      string     `json:"currency"`
	QuantityTotal *int       `json:"quantity_total"`
	QuantitySold  int        `json:"quantity_sold"`
	SalesStart    *time.Time `json:"sales_start"`
	SalesEnd      *time.Time `json:"sales_end"`
	IsActive      bool       `json:"is_active"`
	SoldOut       bool       `json:"sold_out"`
	SalesOpen     bool       `json:"sales_open"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
