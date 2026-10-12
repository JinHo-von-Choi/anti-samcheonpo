package ledger

// DeliveryChannels supplies historical routing facts without rerunning policy.
func (d *DB) DeliveryChannels(id string) (map[string]string, error) {
	rows, err := d.Query(`SELECT verdict_id,channel FROM intervention WHERE session_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var verdict, channel string
		if err = rows.Scan(&verdict, &channel); err != nil {
			return nil, err
		}
		out[verdict] = channel
	}
	return out, rows.Err()
}
