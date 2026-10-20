package retention

import (
	"context"
)

func enforceRepoTotal(options Options, report *Report) ClassReport {
	return enforceRepoTotalContext(context.Background(), options, report)
}
