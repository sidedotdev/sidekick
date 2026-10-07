package com.example.app.core.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class Rfc3339OrderTest {
    @Test
    fun `fractional seconds sort after the whole second they extend`() {
        assertBefore("2024-01-01T00:00:01Z", "2024-01-01T00:00:01.5Z")
        assertBefore("2024-01-01T00:00:01.25Z", "2024-01-01T00:00:01.5Z")
        assertBefore("2024-01-01T00:00:01.999999999Z", "2024-01-01T00:00:02Z")
    }

    @Test
    fun `nanosecond precision is preserved`() {
        assertBefore("2024-01-01T00:00:01Z", "2024-01-01T00:00:01.000000001Z")
        assertBefore("2024-01-01T00:00:01.000000001Z", "2024-01-01T00:00:01.000000002Z")
    }

    @Test
    fun `equal instants compare equal regardless of offset or trailing zeros`() {
        assertSame("2024-01-01T00:00:01Z", "2024-01-01T00:00:01.000Z")
        assertSame("2024-01-01T00:00:01Z", "2024-01-01T02:00:01+02:00")
        assertSame("2024-01-01T00:00:00Z", "2023-12-31T19:00:00-05:00")
        assertSame("2024-01-01T00:00:01.5Z", "2024-01-01T00:00:01.500000000+00:00")
    }

    @Test
    fun `calendar arithmetic handles month, year and leap day boundaries`() {
        assertBefore("2024-02-29T23:59:59Z", "2024-03-01T00:00:00Z")
        assertBefore("2023-12-31T23:59:59Z", "2024-01-01T00:00:00Z")
        assertBefore("1969-12-31T23:59:59Z", "1970-01-01T00:00:00Z")
        assertBefore("2000-02-28T00:00:00Z", "2000-02-29T00:00:00Z")
    }

    @Test
    fun `strings that are not timestamps sort before every instant and lexically among themselves`() {
        assertBefore("", "2024-01-01T00:00:00Z")
        assertBefore("a", "b")
        assertBefore("not a timestamp", "2024-01-01T00:00:00Z")
        assertBefore("2024-13-01T00:00:00Z", "2024-01-01T00:00:00Z")
        assertBefore("2024-01-01T25:00:00Z", "2024-01-01T00:00:00Z")
        assertBefore("2024-02-30T00:00:00Z", "2024-01-01T00:00:00Z")
        assertSame("garbage", "garbage")
    }

    @Test
    fun `ordering is transitive when invalid strings mix with offset timestamps`() {
        // Lexically, "1" < "2" < "z" but the two timestamps are the same instant, so a
        // pairwise lexical fallback would make the comparator inconsistent.
        val values = listOf("2024-01-01T02:00:00+02:00", "1 not a timestamp", "2024-01-01T00:00:00Z", "z")
        for (a in values) for (b in values) for (c in values) {
            val ab = Rfc3339Order.compare(a, b)
            val bc = Rfc3339Order.compare(b, c)
            val ac = Rfc3339Order.compare(a, c)
            if (ab <= 0 && bc <= 0) assertTrue("$a <= $b <= $c but $a > $c", ac <= 0)
            if (ab == 0 && bc == 0) assertEquals("$a == $b == $c", 0, ac)
        }
    }

    private fun assertBefore(earlier: String, later: String) {
        assertTrue("$earlier should sort before $later", Rfc3339Order.compare(earlier, later) < 0)
        assertTrue("$later should sort after $earlier", Rfc3339Order.compare(later, earlier) > 0)
    }

    private fun assertSame(a: String, b: String) {
        assertEquals("$a should equal $b", 0, Rfc3339Order.compare(a, b))
        assertEquals("$b should equal $a", 0, Rfc3339Order.compare(b, a))
    }
}