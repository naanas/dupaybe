package service

import "testing"

func TestMapStatusWholeWords(t *testing.T) {
	tripay := map[string]interface{}{
		"success_statuses":  []interface{}{"PAID"},
		"failed_statuses":   []interface{}{"EXPIRED", "FAILED"},
		"refunded_statuses": []interface{}{"REFUND"},
	}
	ipaymu := map[string]interface{}{
		"success_statuses":  []interface{}{"berhasil", "1", "6"},
		"failed_statuses":   []interface{}{"expired", "-2", "2", "4", "5"},
		"refunded_statuses": []interface{}{"3"},
	}
	cases := []struct {
		name    string
		mapping map[string]interface{}
		in      string
		want    string
	}{
		{"tripay paid", tripay, "PAID", "SUCCESS"},
		{"tripay unpaid bukan success", tripay, "UNPAID", "PENDING"},
		{"tripay check-status unpaid", tripay, "Status transaksi saat ini UNPAID", "PENDING"},
		{"tripay check-status paid", tripay, "Status transaksi saat ini PAID", "SUCCESS"},
		{"tripay expired", tripay, "EXPIRED", "FAILED"},
		{"tripay refund", tripay, "REFUND", "REFUNDED"},
		{"belum dibayar", nil, "Transaksi BELUM DIBAYAR", "PENDING"},
		{"sudah dibayar", nil, "Transaksi sudah dibayar", "SUCCESS"},
		{"kosong", tripay, "", "PENDING"},
		{"ipaymu 1", ipaymu, "1", "SUCCESS"},
		{"ipaymu 0 pending", ipaymu, "0", "PENDING"},
		{"ipaymu -2", ipaymu, "-2", "FAILED"},
		{"ipaymu 3 refund", ipaymu, "3", "REFUNDED"},
		{"ipaymu 10 bukan 1", ipaymu, "10", "PENDING"},
		{"ipaymu berhasil", ipaymu, "berhasil", "SUCCESS"},
	}
	for _, c := range cases {
		if got := mapStatus(c.mapping, c.in); got != c.want {
			t.Errorf("%s: mapStatus(%q) = %s, mau %s", c.name, c.in, got, c.want)
		}
	}
}

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"PENDING", "SUCCESS", true},
		{"PENDING", "FAILED", true},
		{"SUCCESS", "FAILED", false},
		{"SUCCESS", "PENDING", false},
		{"SUCCESS", "REFUNDED", true},
		{"FAILED", "SUCCESS", false},
		{"REFUNDED", "SUCCESS", false},
		{"PENDING", "PENDING", false},
	}
	for _, c := range cases {
		if got := canTransition(c.from, c.to); got != c.want {
			t.Errorf("canTransition(%s,%s) = %v, mau %v", c.from, c.to, got, c.want)
		}
	}
}

func TestAmountShort(t *testing.T) {
	if short, _ := amountShort(`{"total_amount":9000}`, "total_amount", 10000); !short {
		t.Error("9000 < 10000 harus ditolak")
	}
	if short, _ := amountShort(`{"total_amount":10750}`, "total_amount", 10000); short {
		t.Error("10750 (termasuk fee customer) harus lolos")
	}
	if short, _ := amountShort(`total=9000&status=1`, "total", 10000); !short {
		t.Error("form-urlencoded juga dicek")
	}
	if short, _ := amountShort(`{"x":1}`, "", 10000); short {
		t.Error("tanpa amount_path nggak dicek")
	}
}
