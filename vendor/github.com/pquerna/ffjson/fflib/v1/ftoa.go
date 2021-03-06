package v1

/**
 *  Copyright 2015 Paul Querna, Klaus Post
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 *
 */

/* Most of this file are on Go stdlib's strconv/ftoa.go */
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

import "math"

// TODO: move elsewhere?
type floatInfo struct {
	mantbits uint
	expbits  uint
	bias     int
}

var optimize = true // can change for testing

var float32info = floatInfo{23, 8, -127}
var float64info = floatInfo{52, 11, -1023}

// AppendFloat appends the string form of the floating-point number f,
// as generated by FormatFloat
func AppendFloat(dst EncodingBuffer, val float64, fmt byte, prec, bitSize int) {
	var bits uint64
	var flt *floatInfo
	switch bitSize {
	case 32:
		bits = uint64(math.Float32bits(float32(val)))
		flt = &float32info
	case 64:
		bits = math.Float64bits(val)
		flt = &float64info
	default:
		panic("strconv: illegal AppendFloat/FormatFloat bitSize")
	}

	neg := bits>>(flt.expbits+flt.mantbits) != 0
	exp := int(bits>>flt.mantbits) & (1<<flt.expbits - 1)
	mant := bits & (uint64(1)<<flt.mantbits - 1)

	switch exp {
	case 1<<flt.expbits - 1:
		// Inf, NaN
		var s string
		switch {
		case mant != 0:
			s = "NaN"
		case neg:
			s = "-Inf"
		default:
			s = "+Inf"
		}
		dst.WriteString(s)
		return

	case 0:
		// denormalized
		exp++

	default:
		// add implicit top bit
		mant |= uint64(1) << flt.mantbits
	}
	exp += flt.bias

	// Pick off easy binary format.
	if fmt == 'b' {
		fmtB(dst, neg, mant, exp, flt)
		return
	}

	if !optimize {
		bigFtoa(dst, prec, fmt, neg, mant, exp, flt)
		return
	}

	var digs decimalSlice
	ok := false
	// Negative precision means "only as much as needed to be exact."
	shortest := prec < 0
	if shortest {
		// Try Grisu3 algorithm.
		f := new(extFloat)
		lower, upper := f.AssignComputeBounds(mant, exp, neg, flt)
		var buf [32]byte
		digs.d = buf[:]
		ok = f.ShortestDecimal(&digs, &lower, &upper)
		if !ok {
			bigFtoa(dst, prec, fmt, neg, mant, exp, flt)
			return
		}
		// Precision for shortest representation mode.
		switch fmt {
		case 'e', 'E':
			prec = max(digs.nd-1, 0)
		case 'f':
			prec = max(digs.nd-digs.dp, 0)
		case 'g', 'G':
			prec = digs.nd
		}
	} else if fmt != 'f' {
		// Fixed number of digits.
		digits := prec
		switch fmt {
		case 'e', 'E':
			digits++
		case 'g', 'G':
			if prec == 0 {
				prec = 1
			}
			digits = prec
		}
		if digits <= 15 {
			// try fast algorithm when the number of digits is reasonable.
			var buf [24]byte
			digs.d = buf[:]
			f := extFloat{mant, exp - int(flt.mantbits), neg}
			ok = f.FixedDecimal(&digs, digits)
		}
	}
	if !ok {
		bigFtoa(dst, prec, fmt, neg, mant, exp, flt)
		return
	}
	formatDigits(dst, shortest, neg, digs, prec, fmt)
	return
}

// bigFtoa uses multiprecision computations to format a float.
func bigFtoa(dst EncodingBuffer, prec int, fmt byte, neg bool, mant uint64, exp int, flt *floatInfo) {
	d := new(decimal)
	d.Assign(mant)
	d.Shift(exp - int(flt.mantbits))
	var digs decimalSlice
	shortest := prec < 0
	if shortest {
		roundShortest(d, mant, exp, flt)
		digs = decimalSlice{d: d.d[:], nd: d.nd, dp: d.dp}
		// Precision for shortest representation mode.
		switch fmt {
		case 'e', 'E':
			prec = digs.nd - 1
		case 'f':
			prec = max(digs.nd-digs.dp, 0)
		case 'g', 'G':
			prec = digs.nd
		}
	} else {
		// Round appropriately.
		switch fmt {
		case 'e', 'E':
			d.Round(prec + 1)
		case 'f':
			d.Round(d.dp + prec)
		case 'g', 'G':
			if prec == 0 {
				prec = 1
			}
			d.Round(prec)
		}
		digs = decimalSlice{d: d.d[:], nd: d.nd, dp: d.dp}
	}
	formatDigits(dst, shortest, neg, digs, prec, fmt)
	return
}

func formatDigits(dst EncodingBuffer, shortest bool, neg bool, digs decimalSlice, prec int, fmt byte) {
	switch fmt {
	case 'e', 'E':
		fmtE(dst, neg, digs, prec, fmt)
		return
	case 'f':
		fmtF(dst, neg, digs, prec)
		return
	case 'g', 'G':
		// trailing fractional zeros in 'e' form will be trimmed.
		eprec := prec
		if eprec > digs.nd && digs.nd >= digs.dp {
			eprec = digs.nd
		}
		// %e is used if the exponent from the conversion
		// is less than -4 or greater than or equal to the precision.
		// if precision was the shortest possible, use precision 6 for this decision.
		if shortest {
			eprec = 6
		}
		exp := digs.dp - 1
		if exp < -4 || exp >= eprec {
			if prec > digs.nd {
				prec = digs.nd
			}
			fmtE(dst, neg, digs, prec-1, fmt+'e'-'g')
			return
		}
		if prec > digs.dp {
			prec = digs.nd
		}
		fmtF(dst, neg, digs, max(prec-digs.dp, 0))
		return
	}

	// unknown format
	dst.Write([]byte{'%', fmt})
	return
}

// Round d (= mant * 2^exp) to the shortest number of digits
// that will let the original floating point value be precisely
// reconstructed.  Size is original floating point size (64 or 32).
func roundShortest(d *decimal, mant uint64, exp int, flt *floatInfo) {
	// If mantissa is zero, the number is zero; stop now.
	if mant == 0 {
		d.nd = 0
		return
	}

	// Compute upper and lower such that any decimal number
	// between upper and lower (possibly inclusive)
	// will round to the original floating point number.

	// We may see at once that the number is already shortest.
	//
	// Suppose d is not denormal, so that 2^exp <= d < 10^dp.
	// The closest shorter number is at least 10^(dp-nd) away.
	// The lower/upper bounds computed below are at distance
	// at most 2^(exp-mantbits).
	//
	// So the number is already shortest if 10^(dp-nd) > 2^(exp-mantbits),
	// or equivalently log2(10)*(dp-nd) > exp-mantbits.
	// It is true if 332/100*(dp-nd) >= exp-mantbits (log2(10) > 3.32).
	minexp := flt.bias + 1 // minimum possible exponent
	if exp > minexp && 332*(d.dp-d.nd) >= 100*(exp-int(flt.mantbits)) {
		// The number is already shortest.
		return
	}

	// d = mant << (exp - mantbits)
	// Next highest floating point number is mant+1 << exp-mantbits.
	// Our upper bound is halfway between, mant*2+1 << exp-mantbits-1.
	upper := new(decimal)
	upper.Assign(mant*2 + 1)
	upper.Shift(exp - int(flt.mantbits) - 1)

	// d = mant << (exp - mantbits)
	// Next lowest floating point number is mant-1 << exp-mantbits,
	// unless mant-1 drops the significant bit and exp is not the minimum exp,
	// in which case the next lowest is mant*2-1 << exp-mantbits-1.
	// Either way, call it mantlo << explo-mantbits.
	// Our lower bound is halfway between, mantlo*2+1 << explo-mantbits-1.
	var mantlo uint64
	var explo int
	if mant > 1<<flt.mantbits || exp == minexp {
		mantlo = mant - 1
		explo = exp
	} else {
		mantlo = mant*2 - 1
		explo = exp - 1
	}
	lower := new(decimal)
	lower.Assign(mantlo*2 + 1)
	lower.Shift(explo - int(flt.mantbits) - 1)

	// The upper and lower bounds are possible outputs only if
	// the original mantissa is even, so that IEEE round-to-even
	// would round to the original mantissa and not the neighbors.
	inclusive := mant%2 == 0

	// Now we can figure out the minimum number of digits required.
	// Walk along until d has distinguished itself from upper and lower.
	for i := 0; i < d.nd; i++ {
		var l, m, u byte // lower, middle, upper digits
		if i < lower.nd {
			l = lower.d[i]
		} else {
			l = '0'
		}
		m = d.d[i]
		if i < upper.nd {
			u = upper.d[i]
		} else {
			u = '0'
		}

		// Okay to round down (truncate) if lower has a different digit
		// or if lower is inclusive and is exactly the result of rounding down.
		okdown := l != m || (inclusive && l == m && i+1 == lower.nd)

		// Okay to round up if upper has a different digit and
		// either upper is inclusive or upper is bigger than the result of rounding up.
		okup := m != u && (inclusive || m+1 < u || i+1 < upper.nd)

		// If it's okay to do either, then round to the nearest one.
		// If it's okay to do only one, do it.
		switch {
		case okdown && okup:
			d.Round(i + 1)
			return
		case okdown:
			d.RoundDown(i + 1)
			return
		case okup:
			d.RoundUp(i + 1)
			return
		}
	}
}

type decimalSlice struct {
	d      []byte
	nd, dp int
	neg    bool
}

// %e: -d.ddddde??dd
func fmtE(dst EncodingBuffer, neg bool, d decimalSlice, prec int, fmt byte) {
	// sign
	if neg {
		dst.WriteByte('-')
	}

	// first digit
	ch := byte('0')
	if d.nd != 0 {
		ch = d.d[0]
	}
	dst.WriteByte(ch)

	// .moredigits
	if prec > 0 {
		dst.WriteByte('.')
		i := 1
		m := min(d.nd, prec+1)
		if i < m {
			dst.Write(d.d[i:m])
			i = m
		}
		for i <= prec {
			dst.WriteByte('0')
			i++
		}
	}

	// e??
	dst.WriteByte(fmt)
	exp := d.dp - 1
	if d.nd == 0 { // special case: 0 has exponent 0
		exp = 0
	}
	if exp < 0 {
		ch = '-'
		exp = -exp
	} else {
		ch = '+'
	}
	dst.WriteByte(ch)

	// dd or ddd
	switch {
	case exp < 10:
		dst.WriteByte('0')
		dst.WriteByte(byte(exp) + '0')
	case exp < 100:
		dst.WriteByte(byte(exp/10) + '0')
		dst.WriteByte(byte(exp%10) + '0')
	default:
		dst.WriteByte(byte(exp/100) + '0')
		dst.WriteByte(byte(exp/10)%10 + '0')
		dst.WriteByte(byte(exp%10) + '0')
	}

	return
}

// %f: -ddddddd.ddddd
func fmtF(dst EncodingBuffer, neg bool, d decimalSlice, prec int) {
	// sign
	if neg {
		dst.WriteByte('-')
	}

	// integer, padded with zeros as needed.
	if d.dp > 0 {
		m := min(d.nd, d.dp)
		dst.Write(d.d[:m])
		for ; m < d.dp; m++ {
			dst.WriteByte('0')
		}
	} else {
		dst.WriteByte('0')
	}

	// fraction
	if prec > 0 {
		dst.WriteByte('.')
		for i := 0; i < prec; i++ {
			ch := byte('0')
			if j := d.dp + i; 0 <= j && j < d.nd {
				ch = d.d[j]
			}
			dst.WriteByte(ch)
		}
	}

	return
}

// %b: -ddddddddp??ddd
func fmtB(dst EncodingBuffer, neg bool, mant uint64, exp int, flt *floatInfo) {
	// sign
	if neg {
		dst.WriteByte('-')
	}

	// mantissa
	formatBits(dst, mant, 10, false)

	// p
	dst.WriteByte('p')

	// ??exponent
	exp -= int(flt.mantbits)
	if exp >= 0 {
		dst.WriteByte('+')
	}
	formatBits(dst, uint64(exp), 10, exp < 0)

	return
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// formatBits computes the string representation of u in the given base.
// If neg is set, u is treated as negative int64 value.
func formatBits(dst EncodingBuffer, u uint64, base int, neg bool) {
	if base < 2 || base > len(digits) {
		panic("strconv: illegal AppendInt/FormatInt base")
	}
	// 2 <= base && base <= len(digits)

	var a [64 + 1]byte // +1 for sign of 64bit value in base 2
	i := len(a)

	if neg {
		u = -u
	}

	// convert bits
	if base == 10 {
		// common case: use constants for / because
		// the compiler can optimize it into a multiply+shift

		if ^uintptr(0)>>32 == 0 {
			for u > uint64(^uintptr(0)) {
				q := u / 1e9
				us := uintptr(u - q*1e9) // us % 1e9 fits into a uintptr
				for j := 9; j > 0; j-- {
					i--
					qs := us / 10
					a[i] = byte(us - qs*10 + '0')
					us = qs
				}
				u = q
			}
		}

		// u guaranteed to fit into a uintptr
		us := uintptr(u)
		for us >= 10 {
			i--
			q := us / 10
			a[i] = byte(us - q*10 + '0')
			us = q
		}
		// u < 10
		i--
		a[i] = byte(us + '0')

	} else if s := shifts[base]; s > 0 {
		// base is power of 2: use shifts and masks instead of / and %
		b := uint64(base)
		m := uintptr(b) - 1 // == 1<<s - 1
		for u >= b {
			i--
			a[i] = digits[uintptr(u)&m]
			u >>= s
		}
		// u < base
		i--
		a[i] = digits[uintptr(u)]

	} else {
		// general case
		b := uint64(base)
		for u >= b {
			i--
			q := u / b
			a[i] = digits[uintptr(u-q*b)]
			u = q
		}
		// u < base
		i--
		a[i] = digits[uintptr(u)]
	}

	// add sign, if any
	if neg {
		i--
		a[i] = '-'
	}

	dst.Write(a[i:])
}
