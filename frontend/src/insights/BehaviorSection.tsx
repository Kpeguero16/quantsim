/**
 * What the account's trading looks like: how much of it there was, which
 * behavioural rules fired, and the band those two put it in.
 */
import { formatQuantity } from '../format'
import type {
  BehaviorFigures,
  BehaviorSection as BehaviorSectionData,
  Finding,
} from '../api/types'
import NarrativeBlock from './NarrativeBlock'
import SectionNotice from './SectionNotice'
import Stat from './Stat'

/** warn is a finding worth acting on; info is an observation. Design
 * tokens, never raw hex -- index.css owns the palette. */
const SEVERITY_CLASS: Record<Finding['severity'], string> = {
  warn: 'text-down',
  info: 'text-ink-muted',
}

const PROFILE_LABEL: Record<
  NonNullable<BehaviorFigures['risk_profile']>,
  string
> = {
  aggressive: 'Aggressive',
  moderate: 'Moderate',
  conservative: 'Conservative',
}

interface BehaviorSectionProps {
  section: BehaviorSectionData
  /** This section's prose, when there is prose to show. Undefined covers
   * three different situations that all render the same way: no narrative
   * was generated, the narrative did not describe this section, or the
   * narrative describes a report that is no longer on screen. The panel
   * decides which -- narrativeView is the single producer -- and by the
   * time it reaches here the only question left is whether to render it. */
  narrative?: string
}

export default function BehaviorSection({ section, narrative }: BehaviorSectionProps) {
  if (section.state !== 'ok') {
    return <SectionNotice heading="Behavior" reason={section.reason} />
  }

  return (
    <section className="rounded-lg border border-line bg-surface">
      <h3 className="border-b border-line px-4 py-3 text-sm font-semibold text-ink">
        Behavior
      </h3>

      <div className="grid grid-cols-2 gap-4 p-4">
        {/* KindCount, which the backend renders grouped -- so 1234 trades
            reads "1,234" on both sides. formatQuantity is that rule. */}
        <Stat label="Trades" value={formatQuantity(section.trade_count)} />
        {/* Absent means never measured, not "unknown" and not a default
            band: risk_profile is derived from the other two sections, so
            when they could not be computed there was nothing to derive it
            from (SPEC.md 2.5). Nothing is rendered rather than a
            placeholder that would read as a finding. */}
        {section.risk_profile && (
          <Stat
            label="Risk profile"
            value={PROFILE_LABEL[section.risk_profile]}
          />
        )}
      </div>

      {section.findings.length > 0 && (
        <ul className="border-t border-line">
          {section.findings.map((finding) => (
            <li
              key={finding.code}
              className="border-b border-line px-4 py-3 last:border-b-0"
            >
              {/* detail is prose for a human and is never parsed -- the
                  backend says so at the struct, and this is the layer that
                  would be tempted to. It already contains the figures this
                  finding is about, rendered by Go. */}
              <p className={`text-sm ${SEVERITY_CLASS[finding.severity]}`}>
                {finding.detail}
              </p>
              {/* turnover_ratio and occurrences are deliberately NOT
                  rendered, even though the type carries them. Both are
                  already inside `detail`, written by Go: overtrading's
                  reads "... 3.4x average equity traded" and panic
                  selling's reads "2 of 5 sells ...". Printing them again
                  beneath would put the same figure on screen twice -- and
                  in the turnover case in two different spellings, since
                  the sentence uses one decimal and a KindRatio formatter
                  uses two. "3.4x" above "3.40" is precisely the disagreement
                  this step exists to prevent, arrived at by helpfulness
                  rather than by a model.

                  They exist on the struct so the NARRATIVE can name them as
                  placeholders, which is a different consumer with a
                  different need.

                  evidence_trade_ids is not rendered, not linked and NOT
                  COUNTED either: a count derived here would be a figure this
                  layer computed (SPEC.md 2.8). */}
            </li>
          ))}
        </ul>
      )}

      <NarrativeBlock text={narrative} />
    </section>
  )
}
