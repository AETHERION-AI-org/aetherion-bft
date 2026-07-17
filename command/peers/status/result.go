package status

import (
	"bytes"
	"fmt"

	"github.com/0xPolygon/polygon-edge/command/helper"
)

type PeersStatusResult struct {
	ID          string   `json:"id"`
	Protocols   []string `json:"protocols"`
	Addresses   []string `json:"addresses"`
	LatestBlock uint64   `json:"latestBlock"`
}

// formatBlock keeps "not announced yet" distinct from "block zero". A peer that has
// said nothing is not a peer sitting at genesis, and reporting 0 for both would make a
// silent peer look like a stuck one.
func formatBlock(n uint64) string {
	if n == 0 {
		return "not announced yet"
	}

	return fmt.Sprintf("%d", n)
}

func (r *PeersStatusResult) GetOutput() string {
	var buffer bytes.Buffer

	buffer.WriteString("\n[PEER STATUS]\n")
	buffer.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("ID|%s", r.ID),
		fmt.Sprintf("Protocols|%s", r.Protocols),
		fmt.Sprintf("Addresses|%s", r.Addresses),
		fmt.Sprintf("Latest block|%s", formatBlock(r.LatestBlock)),
	}))
	buffer.WriteString("\n")

	return buffer.String()
}
