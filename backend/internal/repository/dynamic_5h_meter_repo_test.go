//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDynamic5hMeterEventSharesBillingTransaction(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[failed], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			cmd := &service.UsageBillingCommand{RequestID: "req", APIKeyID: 2, UserID: 7, Dynamic5hMeterUnits: 25}
			cmd.Normalize()
			mock.ExpectBegin()
			mock.ExpectQuery("INSERT INTO usage_billing_dedup").WithArgs("req", int64(2), cmd.RequestFingerprint).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			mock.ExpectQuery("SELECT request_fingerprint").WithArgs("req", int64(2)).WillReturnError(sql.ErrNoRows)
			insert := mock.ExpectQuery("INSERT INTO dynamic_5h_meter_events").WithArgs("billing:2:req", int64(7), "req", float64(25))
			if failed {
				insert.WillReturnError(errors.New("ledger write failed"))
				mock.ExpectRollback()
			} else {
				insert.WillReturnRows(sqlmock.NewRows([]string{"id", "occurred_at"}).AddRow(10, time.Now().UTC()))
				mock.ExpectCommit()
			}
			result, err := (&usageBillingRepository{db: db}).Apply(context.Background(), cmd)
			if failed {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 10, result.Dynamic5hMeterEvent.ID)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
