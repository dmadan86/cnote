//! Pure GST math (ADR-008). Money is i64 paise, quantity is a decimal; no floats anywhere.

use chrono::{Datelike, NaiveDate};
use rust_decimal::prelude::*;

/// (code, name). 25 is the pre-2020 Daman & Diu code, still present on old GSTINs.
pub const STATES: &[(&str, &str)] = &[
    ("01", "Jammu and Kashmir"),
    ("02", "Himachal Pradesh"),
    ("03", "Punjab"),
    ("04", "Chandigarh"),
    ("05", "Uttarakhand"),
    ("06", "Haryana"),
    ("07", "Delhi"),
    ("08", "Rajasthan"),
    ("09", "Uttar Pradesh"),
    ("10", "Bihar"),
    ("11", "Sikkim"),
    ("12", "Arunachal Pradesh"),
    ("13", "Nagaland"),
    ("14", "Manipur"),
    ("15", "Mizoram"),
    ("16", "Tripura"),
    ("17", "Meghalaya"),
    ("18", "Assam"),
    ("19", "West Bengal"),
    ("20", "Jharkhand"),
    ("21", "Odisha"),
    ("22", "Chhattisgarh"),
    ("23", "Madhya Pradesh"),
    ("24", "Gujarat"),
    ("25", "Daman and Diu"),
    ("26", "Dadra and Nagar Haveli and Daman and Diu"),
    ("27", "Maharashtra"),
    ("28", "Andhra Pradesh (before division)"),
    ("29", "Karnataka"),
    ("30", "Goa"),
    ("31", "Lakshadweep"),
    ("32", "Kerala"),
    ("33", "Tamil Nadu"),
    ("34", "Puducherry"),
    ("35", "Andaman and Nicobar Islands"),
    ("36", "Telangana"),
    ("37", "Andhra Pradesh"),
    ("38", "Ladakh"),
    ("97", "Other Territory"),
    ("99", "Centre Jurisdiction"),
];

pub fn state_name(code: &str) -> Option<&'static str> {
    STATES.iter().find(|(c, _)| *c == code).map(|(_, n)| *n)
}

#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum GstError {
    #[error("GSTIN must be 15 characters")]
    GstinLength,
    #[error("GSTIN format is invalid")]
    GstinFormat,
    #[error("GSTIN has unknown state code {0}")]
    GstinState(String),
    #[error("GSTIN checksum does not match")]
    GstinChecksum,
    #[error("unknown GST state code {0}")]
    State(String),
    #[error("quantity must be greater than zero")]
    Quantity,
    #[error("unit price cannot be negative")]
    Price,
    #[error("GST rate must be between 0 and 28% (basis points 0-2800)")]
    Rate,
    #[error("HSN/SAC must be 4, 6 or 8 digits")]
    Hsn,
    #[error("amount out of range")]
    Overflow,
}

const B36: &[u8; 36] = b"0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ";

/// Check character for the first 14 characters of a GSTIN (mod-36 weighted sum).
fn gstin_check_char(first14: &[u8]) -> char {
    let sum: usize = first14
        .iter()
        .enumerate()
        .map(|(i, c)| {
            let v = B36.iter().position(|x| x == c).unwrap_or(0) * if i % 2 == 0 { 1 } else { 2 };
            v / 36 + v % 36
        })
        .sum();
    B36[(36 - sum % 36) % 36] as char
}

/// Append the check character to a 14-character GSTIN prefix.
pub fn complete_gstin(first14: &str) -> String {
    let p = first14.to_uppercase();
    format!("{p}{}", gstin_check_char(p.as_bytes()))
}

/// Validate and normalise (trim, uppercase) a GSTIN: layout, state code and checksum.
pub fn validate_gstin(s: &str) -> Result<String, GstError> {
    let g = s.trim().to_uppercase();
    let b = g.as_bytes();
    if b.len() != 15 {
        return Err(GstError::GstinLength);
    }
    let ok = b[0..2].iter().all(u8::is_ascii_digit)
        && b[2..7].iter().all(u8::is_ascii_uppercase)
        && b[7..11].iter().all(u8::is_ascii_digit)
        && b[11].is_ascii_uppercase()
        && b[12] != b'0'
        && b[12].is_ascii_alphanumeric()
        && b[13] == b'Z'
        && b[14].is_ascii_alphanumeric();
    if !ok {
        return Err(GstError::GstinFormat);
    }
    if state_name(&g[0..2]).is_none() {
        return Err(GstError::GstinState(g[0..2].to_string()));
    }
    if gstin_check_char(&b[..14]) != b[14] as char {
        return Err(GstError::GstinChecksum);
    }
    Ok(g)
}

pub fn gstin_state(gstin: &str) -> &str {
    gstin.get(0..2).unwrap_or("")
}

pub fn validate_state(code: &str) -> Result<(), GstError> {
    state_name(code)
        .map(|_| ())
        .ok_or_else(|| GstError::State(code.to_string()))
}

pub fn validate_hsn(h: &str) -> Result<(), GstError> {
    if matches!(h.len(), 4 | 6 | 8) && h.bytes().all(|c| c.is_ascii_digit()) {
        Ok(())
    } else {
        Err(GstError::Hsn)
    }
}

/// Indian financial year (April-March) label for a date, e.g. 2026-11-10 -> "2026-27".
pub fn financial_year(d: NaiveDate) -> String {
    let start = if d.month() >= 4 {
        d.year()
    } else {
        d.year() - 1
    };
    format!("{start}-{:02}", (start + 1) % 100)
}

/// qty x unit price in paise, rounded half-up (away from zero; inputs are non-negative).
pub fn taxable_paise(qty: Decimal, unit_price_minor: i64) -> Result<i64, GstError> {
    if qty <= Decimal::ZERO {
        return Err(GstError::Quantity);
    }
    if unit_price_minor < 0 {
        return Err(GstError::Price);
    }
    Decimal::from(unit_price_minor)
        .checked_mul(qty)
        .ok_or(GstError::Overflow)?
        .round_dp_with_strategy(0, RoundingStrategy::MidpointAwayFromZero)
        .to_i64()
        .ok_or(GstError::Overflow)
}

/// taxable x rate (basis points), half-up to paise.
pub fn tax_paise(taxable_minor: i64, rate_bp: u32) -> Result<i64, GstError> {
    let x = taxable_minor as i128 * rate_bp as i128;
    i64::try_from((x + 5_000) / 10_000).map_err(|_| GstError::Overflow)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Supply {
    /// CGST + SGST
    Intra,
    /// IGST
    Inter,
}

pub fn supply_kind(supplier_state: &str, place_of_supply: &str) -> Result<Supply, GstError> {
    validate_state(supplier_state)?;
    validate_state(place_of_supply)?;
    Ok(if supplier_state == place_of_supply {
        Supply::Intra
    } else {
        Supply::Inter
    })
}

#[derive(Debug, Clone, PartialEq)]
pub struct LineIn {
    pub qty: Decimal,
    pub unit_price_minor: i64,
    pub rate_bp: u32,
    pub hsn_sac: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LineTax {
    pub taxable_minor: i64,
    pub cgst_minor: i64,
    pub sgst_minor: i64,
    pub igst_minor: i64,
}

/// Tax for one line. The total tax is rounded once, then split: CGST gets the floor half and SGST the
/// remainder, so the two always add up to the tax and the odd paisa is always SGST.
pub fn line_tax(
    taxable_minor: i64,
    rate_bp: u32,
    supply: Supply,
    reverse_charge: bool,
) -> Result<LineTax, GstError> {
    if rate_bp > 2800 {
        return Err(GstError::Rate);
    }
    let mut t = LineTax {
        taxable_minor,
        cgst_minor: 0,
        sgst_minor: 0,
        igst_minor: 0,
    };
    if reverse_charge {
        return Ok(t);
    }
    let tax = tax_paise(taxable_minor, rate_bp)?;
    match supply {
        Supply::Intra => {
            t.cgst_minor = tax / 2;
            t.sgst_minor = tax - tax / 2;
        }
        Supply::Inter => t.igst_minor = tax,
    }
    Ok(t)
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct InvoiceTax {
    pub lines: Vec<LineTax>,
    pub supply: Supply,
    /// Tax is payable by the recipient: nothing is charged on the invoice, but it is flagged.
    pub reverse_charge: bool,
    pub subtotal_minor: i64,
    pub cgst_minor: i64,
    pub sgst_minor: i64,
    pub igst_minor: i64,
    pub total_minor: i64,
}

pub fn compute_invoice(
    lines: &[LineIn],
    supplier_state: &str,
    place_of_supply: &str,
    reverse_charge: bool,
) -> Result<InvoiceTax, GstError> {
    let supply = supply_kind(supplier_state, place_of_supply)?;
    let mut out = InvoiceTax {
        lines: Vec::with_capacity(lines.len()),
        supply,
        reverse_charge,
        subtotal_minor: 0,
        cgst_minor: 0,
        sgst_minor: 0,
        igst_minor: 0,
        total_minor: 0,
    };
    for l in lines {
        if let Some(h) = &l.hsn_sac {
            validate_hsn(h)?;
        }
        let t = line_tax(
            taxable_paise(l.qty, l.unit_price_minor)?,
            l.rate_bp,
            supply,
            reverse_charge,
        )?;
        for (acc, v) in [
            (&mut out.subtotal_minor, t.taxable_minor),
            (&mut out.cgst_minor, t.cgst_minor),
            (&mut out.sgst_minor, t.sgst_minor),
            (&mut out.igst_minor, t.igst_minor),
        ] {
            *acc = acc.checked_add(v).ok_or(GstError::Overflow)?;
        }
        out.lines.push(t);
    }
    out.total_minor = out.subtotal_minor + out.cgst_minor + out.sgst_minor + out.igst_minor;
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn d(s: &str) -> Decimal {
        s.parse().unwrap()
    }
    fn line(qty: &str, price: i64, rate_bp: u32) -> LineIn {
        LineIn {
            qty: d(qty),
            unit_price_minor: price,
            rate_bp,
            hsn_sac: None,
        }
    }

    #[test]
    fn taxable_rounds_half_up() {
        assert_eq!(taxable_paise(d("1"), 12_345).unwrap(), 12_345);
        assert_eq!(taxable_paise(d("2.5"), 101).unwrap(), 253); // 252.5 -> 253
        assert_eq!(taxable_paise(d("0.5"), 3).unwrap(), 2); // 1.5 -> 2
        assert_eq!(taxable_paise(d("0.333"), 100).unwrap(), 33); // 33.3 -> 33
        assert_eq!(taxable_paise(d("0.335"), 100).unwrap(), 34); // 33.5 -> 34
        assert_eq!(taxable_paise(d("0.001"), 100).unwrap(), 0);
        assert_eq!(taxable_paise(d("10.25"), 0).unwrap(), 0);
    }

    #[test]
    fn taxable_rejects_bad_input() {
        assert_eq!(taxable_paise(d("0"), 1), Err(GstError::Quantity));
        assert_eq!(taxable_paise(d("-1"), 1), Err(GstError::Quantity));
        assert_eq!(taxable_paise(d("1"), -1), Err(GstError::Price));
        assert_eq!(
            taxable_paise(d("1000000000000"), i64::MAX),
            Err(GstError::Overflow)
        );
        assert_eq!(taxable_paise(d("2"), i64::MAX), Err(GstError::Overflow));
    }

    #[test]
    fn tax_rounds_half_up() {
        assert_eq!(tax_paise(10_000, 1800).unwrap(), 1800);
        assert_eq!(tax_paise(50, 500).unwrap(), 3); // 2.5 -> 3
        assert_eq!(tax_paise(49, 500).unwrap(), 2); // 2.45 -> 2
        assert_eq!(tax_paise(1, 1800).unwrap(), 0);
        assert_eq!(tax_paise(3, 1800).unwrap(), 1); // 0.54 -> 1
        assert_eq!(tax_paise(0, 1800).unwrap(), 0);
    }

    #[test]
    fn intra_state_splits_cgst_sgst() {
        let t = line_tax(10_000, 1800, Supply::Intra, false).unwrap();
        assert_eq!((t.cgst_minor, t.sgst_minor, t.igst_minor), (900, 900, 0));
    }

    #[test]
    fn odd_paisa_goes_to_sgst_and_nothing_is_lost() {
        // 18% of 1.05 rupees = 18.9 -> 19 paise: 9 + 10
        let t = line_tax(105, 1800, Supply::Intra, false).unwrap();
        assert_eq!((t.cgst_minor, t.sgst_minor), (9, 10));
        for taxable in 0..2_000 {
            for rate in [0, 25, 300, 500, 1200, 1800, 2800] {
                let t = line_tax(taxable, rate, Supply::Intra, false).unwrap();
                assert_eq!(
                    t.cgst_minor + t.sgst_minor,
                    tax_paise(taxable, rate).unwrap()
                );
                assert!(t.sgst_minor - t.cgst_minor <= 1 && t.sgst_minor >= t.cgst_minor);
            }
        }
    }

    #[test]
    fn inter_state_is_all_igst() {
        let t = line_tax(10_000, 1800, Supply::Inter, false).unwrap();
        assert_eq!((t.cgst_minor, t.sgst_minor, t.igst_minor), (0, 0, 1800));
    }

    #[test]
    fn reverse_charge_charges_no_tax() {
        let t = line_tax(10_000, 1800, Supply::Intra, true).unwrap();
        assert_eq!((t.cgst_minor, t.sgst_minor, t.igst_minor), (0, 0, 0));
        let inv = compute_invoice(&[line("1", 10_000, 1800)], "29", "29", true).unwrap();
        assert!(inv.reverse_charge);
        assert_eq!(inv.total_minor, 10_000);
    }

    #[test]
    fn rate_above_28_percent_rejected() {
        assert_eq!(
            line_tax(100, 2801, Supply::Inter, false),
            Err(GstError::Rate)
        );
    }

    #[test]
    fn invoice_intra_totals() {
        let lines = [line("2", 50_000, 1800), line("1.5", 20_001, 500)];
        let inv = compute_invoice(&lines, "29", "29", false).unwrap();
        assert_eq!(inv.supply, Supply::Intra);
        assert_eq!(inv.lines[0].taxable_minor, 100_000);
        assert_eq!(inv.lines[1].taxable_minor, 30_002); // 30001.5 -> 30002
        assert_eq!(inv.subtotal_minor, 130_002);
        assert_eq!(inv.cgst_minor, 9_000 + 750); // 18% of 100000 = 9000 each; 5% of 30002 = 1500 -> 750 each
        assert_eq!(inv.sgst_minor, 9_000 + 750);
        assert_eq!(inv.igst_minor, 0);
        assert_eq!(
            inv.total_minor,
            inv.subtotal_minor + inv.cgst_minor + inv.sgst_minor
        );
    }

    #[test]
    fn invoice_inter_totals_and_state_errors() {
        let inv = compute_invoice(&[line("1", 100_000, 1200)], "29", "27", false).unwrap();
        assert_eq!(inv.supply, Supply::Inter);
        assert_eq!((inv.igst_minor, inv.total_minor), (12_000, 112_000));
        assert_eq!(
            compute_invoice(&[], "29", "00", false),
            Err(GstError::State("00".into()))
        );
        assert_eq!(
            compute_invoice(&[], "29", "29", false).unwrap().total_minor,
            0
        );
    }

    #[test]
    fn hsn_is_validated_per_line() {
        let mut l = line("1", 100, 1800);
        l.hsn_sac = Some("998314".into());
        assert!(compute_invoice(&[l.clone()], "29", "29", false).is_ok());
        for bad in ["99", "99831", "99831x", "", "123456789"] {
            l.hsn_sac = Some(bad.into());
            assert_eq!(
                compute_invoice(&[l.clone()], "29", "29", false),
                Err(GstError::Hsn)
            );
        }
        assert!(validate_hsn("8471").is_ok() && validate_hsn("84713010").is_ok());
    }

    #[test]
    fn gstin_known_valid_and_roundtrip() {
        assert_eq!(
            validate_gstin("27AAPFU0939F1ZV").unwrap(),
            "27AAPFU0939F1ZV"
        );
        assert_eq!(
            validate_gstin(" 27aapfu0939f1zv ").unwrap(),
            "27AAPFU0939F1ZV"
        );
        for prefix in [
            "29ABCDE1234F1Z",
            "07AAACC1206D1Z",
            "33AABCU9603R1Z",
            "99ABCDE1234F2Z",
        ] {
            let g = complete_gstin(prefix);
            assert_eq!(validate_gstin(&g).unwrap(), g, "{g}");
        }
    }

    #[test]
    fn gstin_rejects_bad_values() {
        assert_eq!(validate_gstin(""), Err(GstError::GstinLength));
        assert_eq!(validate_gstin("27AAPFU0939F1Z"), Err(GstError::GstinLength));
        assert_eq!(
            validate_gstin("27AAPFU0939F1ZVX"),
            Err(GstError::GstinLength)
        );
        assert_eq!(
            validate_gstin("27AAPFU0939F1ZX"),
            Err(GstError::GstinChecksum)
        );
        assert_eq!(
            validate_gstin("2XAAPFU0939F1ZV"),
            Err(GstError::GstinFormat)
        );
        assert_eq!(
            validate_gstin("27AAPF10939F1ZV"),
            Err(GstError::GstinFormat)
        );
        assert_eq!(
            validate_gstin("27AAPFU0939F1YV"),
            Err(GstError::GstinFormat)
        ); // 14th must be Z
        assert_eq!(
            validate_gstin("27AAPFU0939F0ZV"),
            Err(GstError::GstinFormat)
        ); // 13th cannot be 0
        let unknown = complete_gstin("00AAPFU0939F1Z");
        assert_eq!(
            validate_gstin(&unknown),
            Err(GstError::GstinState("00".into()))
        );
        // any single-character corruption of a valid GSTIN in the digits breaks the checksum
        assert_eq!(
            validate_gstin("27AAPFU0938F1ZV"),
            Err(GstError::GstinChecksum)
        );
    }

    #[test]
    fn state_table() {
        assert_eq!(state_name("29"), Some("Karnataka"));
        assert_eq!(state_name("27"), Some("Maharashtra"));
        assert_eq!(state_name("00"), None);
        assert_eq!(state_name("39"), None);
        assert!(STATES.iter().all(|(c, _)| c.len() == 2));
        let mut codes: Vec<_> = STATES.iter().map(|s| s.0).collect();
        codes.sort();
        codes.dedup();
        assert_eq!(codes.len(), STATES.len());
        assert_eq!(gstin_state("29ABCDE1234F1Z5"), "29");
        assert!(validate_state("97").is_ok() && validate_state("abc").is_err());
    }

    #[test]
    fn financial_year_boundaries() {
        let fy = |y, m, d| financial_year(NaiveDate::from_ymd_opt(y, m, d).unwrap());
        assert_eq!(fy(2026, 4, 1), "2026-27");
        assert_eq!(fy(2027, 3, 31), "2026-27");
        assert_eq!(fy(2027, 4, 1), "2027-28");
        assert_eq!(fy(2026, 1, 1), "2025-26");
        assert_eq!(fy(2099, 12, 31), "2099-00");
    }
}
