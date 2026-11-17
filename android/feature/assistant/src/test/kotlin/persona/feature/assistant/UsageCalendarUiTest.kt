package persona.feature.assistant

import android.content.res.Configuration
import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.click
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Machine
import persona.core.contracts.api.UsageDay
import persona.core.contracts.api.UsageDays
import persona.core.contracts.api.UsageHistoryApi
import persona.core.contracts.api.UsageHours
import persona.core.contracts.api.UsageMachineShare
import persona.core.contracts.api.UsageTokens
import persona.core.designsystem.PersonaTheme
import java.io.IOException

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class UsageCalendarUiTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val days = UsageDays(zone = "Asia/Shanghai", stored = true, days = listOf(
        UsageDay(date = "2026-11-16", input = 1, cacheWrite = 2, cacheRead = 4, output = 8),
        UsageDay(date = "2026-11-17", input = 10, cacheWrite = 20, cacheRead = 40, output = 80),
    ))
    private val hours = UsageHours(zone = "Asia/Shanghai", stored = true,
        hours = listOf(persona.core.contracts.api.UsageHour(t = 1_760_000_000, date = "2026-11-17", hour = 9, input = 6, cacheWrite = 8, cacheRead = 10, output = 12)),
        machines = listOf(UsageMachineShare(id = "office", name = "Office", input = 6, cacheWrite = 8, cacheRead = 10, output = 12)),
        total = UsageTokens(input = 6, cacheWrite = 8, cacheRead = 10, output = 12),
    )

    private open class Hub(
        private val days: UsageDays,
        private val hours: UsageHours,
    ) : UsageHistoryApi {
        var dayReads = 0
        var hourReads = 0
        var failure: Exception? = null
        override suspend fun usageDays(days: Int): UsageDays {
            dayReads++
            failure?.let { throw it }
            return this.days
        }
        override suspend fun usageHours(days: Int): UsageHours {
            hourReads++
            failure?.let { throw it }
            return this.hours
        }
    }

    @Test fun aDayCanBeSelectedAndTheCardOpensDetails() {
        var opened = 0
        rule.setContent { PersonaTheme(reduceMotion = true) { UsageCalendarCard(days, onOpen = { opened++ }) } }

        rule.onNodeWithTag("assistant-usage-calendar").assertExists()
        rule.onNodeWithText("Today", substring = true).assertExists()
        rule.onNodeWithText("Last 30 days", substring = true).assertExists()
        rule.onNodeWithTag("usage-day-2026-11-16").performClick()
        rule.onNodeWithText("Nov 16", substring = true).assertExists()
        rule.onNodeWithText("Input", substring = true).assertExists()
        rule.onNodeWithText("Cache write", substring = true).assertExists()
        rule.onNodeWithText("Cache read", substring = true).assertExists()
        rule.onNodeWithText("Output", substring = true).assertExists()

        rule.onNodeWithTag("usage-day-2026-11-16").performClick()
        rule.onNodeWithText("Today", substring = true).assertExists()
        rule.onAllNodesWithText("Cache write", substring = true).assertCountEquals(0)
        rule.onNodeWithTag("usage-calendar-open").performClick()
        assertEquals(1, opened)
    }

    @Test fun homeKeepsChipsUntilDataArrivesThenReplacesThem() {
        val answer = CompletableDeferred<UsageDays>()
        val hub = object : Hub(days, hours) {
            override suspend fun usageDays(days: Int): UsageDays {
                dayReads++
                return answer.await()
            }
        }
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                UsageHomeSection(hub, listOf(Machine("mac", name = "MacBook Pro", online = true)), true, onOpen = {})
            }
        }

        rule.onNodeWithText("Online devices").assertExists()
        rule.onNodeWithText("MacBook Pro").assertExists()
        rule.onAllNodesWithTag("assistant-usage-calendar").assertCountEquals(0)
        rule.runOnIdle { answer.complete(days) }
        rule.waitForIdle()
        rule.onNodeWithTag("assistant-usage-calendar").assertExists()
        rule.onAllNodesWithText("Online devices").assertCountEquals(0)
        rule.onAllNodesWithText("MacBook Pro").assertCountEquals(0)
    }

    @Test fun aFailedFirstReadLeavesTheOriginalDevicesInsteadOfAnEmptyCalendar() {
        val hub = Hub(days, hours).apply { failure = IOException("offline") }
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                UsageHomeSection(hub, listOf(Machine("mac", name = "MacBook Pro", online = true)), true, onOpen = {})
            }
        }

        rule.waitForIdle()
        rule.onNodeWithText("Online devices").assertExists()
        rule.onNodeWithText("MacBook Pro").assertExists()
        rule.onAllNodesWithTag("assistant-usage-calendar").assertCountEquals(0)
    }

    @Test fun detailsShowHoursComputersAndAllFourKinds() {
        val hub = Hub(days, hours)
        rule.setContent { PersonaTheme(reduceMotion = true) { UsageCalendarPage(hub, onBack = {}) } }

        rule.waitForIdle()
        rule.onNodeWithText("Last 7 days by hour", substring = true).assertExists()
        rule.onNodeWithText("By computer", substring = true).assertExists()
        rule.onNodeWithText("Office").assertExists()
        rule.onNodeWithText("By kind", substring = true).assertExists()
        rule.onNodeWithText("Input", substring = true).assertExists()
        rule.onNodeWithText("Cache write", substring = true).assertExists()
        rule.onNodeWithText("Cache read", substring = true).assertExists()
        rule.onNodeWithText("Output", substring = true).assertExists()
    }

    @Test fun disabledDayControlDoesNotOpenDetailsAndEnabledControlStillChangesDay() {
        var opened = 0
        rule.setContent { PersonaTheme(reduceMotion = true) { UsageCalendarCard(days, onOpen = { opened++ }) } }
        rule.onNodeWithTag("usage-day-2026-11-16").performClick()

        rule.onNodeWithContentDescription("Previous day").performTouchInput { click(center) }
        rule.waitForIdle()
        assertEquals("A disabled boundary button must not open details", 0, opened)
        rule.onNodeWithText("Nov 16", substring = true).assertExists()

        rule.onNodeWithContentDescription("Next day").performTouchInput { click(center) }
        rule.waitForIdle()
        rule.onNodeWithText("Nov 17", substring = true).assertExists()
        assertEquals("An enabled day button changes the day without opening details", 0, opened)
    }

    @Test fun largeTextWrapsSummaryInsteadOfEllipsizingTheThirdLabel() {
        largeTextDetails()
        val today = rule.onNodeWithText("Today").getBoundsInRoot().top
        val last7 = rule.onNodeWithText("Last 7 days").getBoundsInRoot().top
        val last30 = rule.onNodeWithText("Last 30 days").getBoundsInRoot().top

        assertEquals("The first two stats stay on one row", today, last7)
        assertTrue("The third stat wraps at 2.0 font scale", last30 > today)
    }

    @Test fun largeTextGivesHourDatesEnoughWidthForTheWholeDay() {
        largeTextDetails()
        val bounds = rule.onNodeWithText("Nov 17").getBoundsInRoot()
        val width = bounds.right - bounds.left
        val required = 112.dp

        assertTrue("The date column grows with the 2.0 font scale: $width < $required", width >= required)
    }

    @Test fun largeTextGivesMonthLabelsEnoughHeight() {
        largeTextDetails()
        val monthTop = rule.onNodeWithText("Nov").getBoundsInRoot().top
        val dayTop = rule.onNodeWithTag("usage-day-2026-11-17").getBoundsInRoot().top
        val rowHeight = dayTop - monthTop

        assertTrue("The month row grows to 40dp at 2.0 font scale: $rowHeight", rowHeight >= 40.dp)
    }

    private fun largeTextDetails() {
        rule.setContent {
            val base = LocalConfiguration.current
            val configuration = Configuration(base).apply { fontScale = 2f }
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(
                    LocalConfiguration provides configuration,
                    LocalDensity provides Density(LocalDensity.current.density, 2f),
                ) {
                    UsageCalendarPage(Hub(days, hours), onBack = {})
                }
            }
        }
        rule.waitForIdle()
    }
}
