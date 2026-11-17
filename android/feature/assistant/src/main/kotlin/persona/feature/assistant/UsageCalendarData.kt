package persona.feature.assistant

import persona.core.contracts.api.UsageDay
import java.time.LocalDate

/** Four quantile steps among the values with usage; ties share their upper rank and no usage is step zero. */
internal fun usageHeatSteps(values: List<Long>): List<Int> {
    val used = values.filter { it > 0 }.sorted()
    return values.map { value ->
        if (value <= 0) 0
        else (4 * used.count { it <= value } + used.size - 1) / used.size
    }
}

/** Complete Monday-first weeks; padding keeps the current week in the last column. */
internal fun usageCalendarWeeks(days: List<UsageDay>): List<List<UsageDay?>> {
    if (days.isEmpty()) return emptyList()
    val leading = LocalDate.parse(days.first().date).dayOfWeek.value - 1
    val padded = List<UsageDay?>(leading) { null } + days
    val trailing = (7 - padded.size % 7) % 7
    return (padded + List<UsageDay?>(trailing) { null }).chunked(7)
}
