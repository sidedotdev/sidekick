package com.example.app.core.data

private const val SECONDS_PER_DAY = 86_400L
private const val NANOS_DIGITS = 9
private const val EPOCH_DAYS_FROM_CIVIL_0000_03_01 = 719_468L
private const val DAYS_PER_400_YEARS = 146_097L

/**
 * Orders RFC 3339 timestamps (as emitted by Go's `time.Time`) by instant, so
 * fractional seconds and UTC offsets compare correctly where plain string
 * comparison would not. Parsing is done here because java.time is unavailable
 * below API 26 without desugaring, and nanosecond precision must be preserved.
 * Strings that don't parse (including out-of-range fields) sort before every
 * valid instant and lexically among themselves, keeping the order total.
 */
internal object Rfc3339Order : Comparator<String> {
    private val pattern = Regex(
        """(\d{4})-(\d{2})-(\d{2})[Tt ](\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(?:[Zz]|([+-])(\d{2}):(\d{2}))""",
    )

    override fun compare(a: String, b: String): Int {
        val first = parseOrNull(a)
        val second = parseOrNull(b)
        return when {
            first != null && second != null -> first.compareTo(second)
            first != null -> 1
            second != null -> -1
            else -> a.compareTo(b)
        }
    }

    private fun parseOrNull(value: String): Instant? {
        val groups = pattern.matchEntire(value)?.groupValues ?: return null
        val year = groups[1].toInt()
        val month = groups[2].toInt()
        val day = groups[3].toInt()
        val hour = groups[4].toInt()
        val minute = groups[5].toInt()
        val second = groups[6].toInt()
        // RFC 3339 permits second 60 for leap seconds.
        if (month !in 1..12 || day !in 1..daysInMonth(year, month) || hour > 23 || minute > 59 || second > 60) {
            return null
        }
        var seconds = daysFromCivil(year, month, day) * SECONDS_PER_DAY + hour * 3600L + minute * 60L + second
        if (groups[8].isNotEmpty()) {
            val offsetHours = groups[9].toInt()
            val offsetMinutes = groups[10].toInt()
            if (offsetHours > 23 || offsetMinutes > 59) return null
            val offsetSeconds = offsetHours * 3600L + offsetMinutes * 60L
            seconds += if (groups[8] == "+") -offsetSeconds else offsetSeconds
        }
        val nanos = groups[7].padEnd(NANOS_DIGITS, '0').toInt()
        return Instant(seconds, nanos)
    }

    private fun daysInMonth(year: Int, month: Int): Int = when (month) {
        2 -> if ((year % 4 == 0 && year % 100 != 0) || year % 400 == 0) 29 else 28
        4, 6, 9, 11 -> 30
        else -> 31
    }

    /** Days since 1970-01-01 for a proleptic Gregorian date (Howard Hinnant's algorithm). */
    private fun daysFromCivil(year: Int, month: Int, day: Int): Long {
        val shiftedYear = if (month <= 2) year - 1 else year
        val era = Math.floorDiv(shiftedYear, 400)
        val yearOfEra = shiftedYear - era * 400
        val dayOfYear = (153 * (if (month > 2) month - 3 else month + 9) + 2) / 5 + day - 1
        val dayOfEra = yearOfEra * 365 + yearOfEra / 4 - yearOfEra / 100 + dayOfYear
        return era * DAYS_PER_400_YEARS + dayOfEra - EPOCH_DAYS_FROM_CIVIL_0000_03_01
    }

    private data class Instant(val epochSeconds: Long, val nanos: Int) : Comparable<Instant> {
        override fun compareTo(other: Instant): Int =
            compareValuesBy(this, other, Instant::epochSeconds, Instant::nanos)
    }
}