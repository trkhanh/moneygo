package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

type Price struct {
	PriceId    int64
	SecurityId int64
	CurrencyId int64
	Date       time.Time
	Value      string // String representation of decimal price of Security in Currency units, suitable for passing to big.Rat.SetString()
	RemoteId   string // unique ID from source, for detecting duplicates
}

type PriceList struct {
	Prices *[]*Price `json:"prices"`
}

func (p *Price) Read(json_str string) error {
	dec := json.NewDecoder(strings.NewReader(json_str))
	return dec.Decode(p)
}

func (p *Price) Write(w http.ResponseWriter) error {
	enc := json.NewEncoder(w)
	return enc.Encode(p)
}

func (pl *PriceList) Read(json_str string) error {
	dec := json.NewDecoder(strings.NewReader(json_str))
	return dec.Decode(pl)
}

func (pl *PriceList) Write(w http.ResponseWriter) error {
	enc := json.NewEncoder(w)
	return enc.Encode(pl)
}

func CreatePriceIfNotExist(tx *Tx, price *Price) error {
	if len(price.RemoteId) == 0 {
		// Always create a new price if we can't match on the RemoteId
		err := tx.Insert(price)
		if err != nil {
			return err
		}
		return nil
	}

	var prices []*Price

	_, err := tx.Select(&prices, "SELECT * from prices where SecurityId=? AND CurrencyId=? AND Date=? AND Value=?", price.SecurityId, price.CurrencyId, price.Date, price.Value)
	if err != nil {
		return err
	}

	if len(prices) > 0 {
		return nil // price already exists
	}

	err = tx.Insert(price)
	if err != nil {
		return err
	}
	return nil
}

func GetPrice(tx *Tx, priceid, userid int64) (*Price, error) {
	var p Price
	err := tx.SelectOne(&p, "SELECT * from prices where PriceId=? AND SecurityId IN (SELECT SecurityId FROM securities WHERE UserId=?)", priceid, userid)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func GetPrices(tx *Tx, userid int64) (*[]*Price, error) {
	var prices []*Price

	_, err := tx.Select(&prices, "SELECT * from prices where SecurityId IN (SELECT SecurityId FROM securities WHERE UserId=?)", userid)
	if err != nil {
		return nil, err
	}
	return &prices, nil
}

// Return the latest price for security in currency units before date
func GetLatestPrice(tx *Tx, security, currency *Security, date *time.Time) (*Price, error) {
	var p Price
	err := tx.SelectOne(&p, "SELECT * from prices where SecurityId=? AND CurrencyId=? AND Date <= ? ORDER BY Date DESC LIMIT 1", security.SecurityId, currency.SecurityId, date)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Return the earliest price for security in currency units after date
func GetEarliestPrice(tx *Tx, security, currency *Security, date *time.Time) (*Price, error) {
	var p Price
	err := tx.SelectOne(&p, "SELECT * from prices where SecurityId=? AND CurrencyId=? AND Date >= ? ORDER BY Date ASC LIMIT 1", security.SecurityId, currency.SecurityId, date)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Return the price for security in currency closest to date
func GetClosestPrice(tx *Tx, security, currency *Security, date *time.Time) (*Price, error) {
	earliest, _ := GetEarliestPrice(tx, security, currency, date)
	latest, err := GetLatestPrice(tx, security, currency, date)

	// Return early if either earliest or latest are invalid
	if earliest == nil {
		return latest, err
	} else if err != nil {
		return earliest, nil
	}

	howlate := earliest.Date.Sub(*date)
	howearly := date.Sub(latest.Date)
	if howearly < howlate {
		return latest, nil
	} else {
		return earliest, nil
	}
}

func PriceHandler(r *http.Request, context *Context) ResponseWriterWriter {
	user, err := GetUserFromSession(context.Tx, r)
	if err != nil {
		return NewError(1 /*Not Signed In*/)
	}

	if r.Method == "POST" {
		price_json := r.PostFormValue("price")
		if price_json == "" {
			return NewError(3 /*Invalid Request*/)
		}

		var price Price
		err := price.Read(price_json)
		if err != nil {
			return NewError(3 /*Invalid Request*/)
		}
		price.PriceId = -1

		_, err = GetSecurity(context.Tx, price.SecurityId, user.UserId)
		if err != nil {
			return NewError(3 /*Invalid Request*/)
		}
		_, err = GetSecurity(context.Tx, price.CurrencyId, user.UserId)
		if err != nil {
			return NewError(3 /*Invalid Request*/)
		}

		err = context.Tx.Insert(&price)
		if err != nil {
			log.Print(err)
			return NewError(999 /*Internal Error*/)
		}

		return ResponseWrapper{201, &price}
	} else if r.Method == "GET" {
		var priceid int64
		n, err := GetURLPieces(r.URL.Path, "/v1/prices/%d", &priceid)

		if err != nil || n != 1 {
			//Return all prices
			var pl PriceList

			prices, err := GetPrices(context.Tx, user.UserId)
			if err != nil {
				log.Print(err)
				return NewError(999 /*Internal Error*/)
			}

			pl.Prices = prices
			return &pl
		} else {
			price, err := GetPrice(context.Tx, priceid, user.UserId)
			if err != nil {
				return NewError(3 /*Invalid Request*/)
			}

			return price
		}
	} else {
		priceid, err := GetURLID(r.URL.Path)
		if err != nil {
			return NewError(3 /*Invalid Request*/)
		}
		if r.Method == "PUT" {
			price_json := r.PostFormValue("price")
			if price_json == "" {
				return NewError(3 /*Invalid Request*/)
			}

			var price Price
			err := price.Read(price_json)
			if err != nil || price.PriceId != priceid {
				return NewError(3 /*Invalid Request*/)
			}

			_, err = GetSecurity(context.Tx, price.SecurityId, user.UserId)
			if err != nil {
				return NewError(3 /*Invalid Request*/)
			}
			_, err = GetSecurity(context.Tx, price.CurrencyId, user.UserId)
			if err != nil {
				return NewError(3 /*Invalid Request*/)
			}

			count, err := context.Tx.Update(&price)
			if err != nil || count != 1 {
				log.Print(err)
				return NewError(999 /*Internal Error*/)
			}

			return &price
		} else if r.Method == "DELETE" {
			price, err := GetPrice(context.Tx, priceid, user.UserId)
			if err != nil {
				return NewError(3 /*Invalid Request*/)
			}

			count, err := context.Tx.Delete(price)
			if err != nil || count != 1 {
				log.Print(err)
				return NewError(999 /*Internal Error*/)
			}

			return SuccessWriter{}
		}
	}
	return NewError(3 /*Invalid Request*/)
}
