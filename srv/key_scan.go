package srv

import (
	"context"
	"fmt"
	"sidekick/common"
)

func (d Delegator) ScanKeys(ctx context.Context, workspaceID, prefix, cursor string, limit int) (common.KeyScanPage, error) {
	scanner, ok := d.storage.(common.SizedKeyScanner)
	if !ok {
		return common.KeyScanPage{}, fmt.Errorf("storage %T does not support sized key scans", d.storage)
	}
	return scanner.ScanKeys(ctx, workspaceID, prefix, cursor, limit)
}
