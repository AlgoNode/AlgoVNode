// Copyright (C) 2022 AlgoNode Org.
//
// algonode is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// algonode is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with algonode.  If not, see <https://www.gnu.org/licenses/>.

package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/algorand/go-algorand/protocol"
	"github.com/algorand/go-codec/codec"
	"github.com/tidwall/jsonc"
)

type eternalFn func(ctx context.Context) (fatal bool, err error)

func Backoff(ctx context.Context, fn eternalFn, timeout time.Duration, wait time.Duration, maxwait time.Duration, tries int64) error {
	//Loop until Algoverse gets cancelled
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		fatal, err := fn(cctx)
		if err == nil { // Success
			cancel()
			return nil
		}
		cancel()
		if fatal {
			return err
		}
		tries--
		if tries == 0 {
			return errors.New("Backoff limit reached")
		}
		//TODO
		//		fmt.Fprintf(os.Stderr, err.Error())

		//keep an eye on cancellation while backing off
		if wait > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
			if maxwait > 0 {
				wait *= 2
				if wait > maxwait {
					wait = maxwait
				}
			}
		}
	}
}

func LoadJSONCFromFile(filename string, object interface{}) (err error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonc.ToJSON(data), &object)
}

func EncodeJson(obj interface{}) ([]byte, error) {
	var output []byte
	enc := codec.NewEncoderBytes(&output, protocol.JSONStrictHandle)

	err := enc.Encode(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to encode object: %v", err)
	}
	return output, nil
}

func PrintableUTF8OrEmpty(in string) string {
	// iterate throughout all the characters in the string to see if they are all printable.
	// when range iterating on go strings, go decode each element as a utf8 rune.
	for _, c := range in {
		// is this a printable character, or invalid rune ?
		if c == utf8.RuneError || !unicode.IsPrint(c) {
			return ""
		}
	}
	return in
}
