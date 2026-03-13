---
name: frontend-design
description: Create distinctive, production-grade frontend interfaces with high design quality. Use this skill when the user asks to build web components, pages, or applications (examples include dashboards, React components, layouts, or when styling/beautifying any web UI). Generates creative, polished code and UI design that avoids generic AI aesthetics.
---

This skill guides creation of distinctive, production-grade frontend interfaces that avoid generic "AI slop" aesthetics. Implement real working code with exceptional attention to aesthetic details and creative choices.

The user provides frontend requirements: a component, page, application, or interface to build. They may include context about the purpose, audience, or technical constraints.

## Tech Stack

This project uses:
- **Next.js** with TypeScript (frontend lives in `client/`)
- **Tailwind CSS** for styling
- **Generated protobuf types** in `client/gen/` for API integration

All frontend code must use these technologies. Use Tailwind utility classes for styling. Use Next.js conventions (App Router, server/client components as appropriate). Import types from `client/gen/` when interfacing with backend data.

## Design Thinking

Before coding, understand the context and commit to a BOLD aesthetic direction:
- **Purpose**: This interface is for individual investors to track their overall financial position (including stock portfolios, cash accounts and other assets), view performance metrics, and set financial goals. The design should make complex financial data feel approachable and even delightful, while conveying trustworthiness and sophistication.
- **Tone**: Minimal, industrial, geometric.
- **Constraints**: Take inspiration from Google's material design principles. Use the tech stack described above.  Ensure that animations are performant and do not cause jank. Animations should be shortcut when run in testing mode.  The interface should be responsive and look great on both desktop and mobile.
- **Differentiation**: This interface should have bold, clear graphs which make headline data feel obvious and available.

**CRITICAL**: Choose a clear conceptual direction and execute it with precision. Bold maximalism and refined minimalism both work - the key is intentionality, not intensity.

Then implement working code that is:
- Production-grade and functional
- Visually striking and memorable
- Cohesive with a clear aesthetic point-of-view
- Meticulously refined in every detail

## Frontend Aesthetics Guidelines

Focus on:
- **Typography**: Choose fonts that are beautiful, unique, and interesting. Avoid generic fonts like Arial and Inter; opt instead for distinctive choices that elevate the frontend's aesthetics; unexpected, characterful font choices. Pair a distinctive display font with a refined body font. Use Google Fonts or Next.js font optimization (`next/font`).
- **Color & Theme**: Commit to a cohesive aesthetic. Use Tailwind's theme extension or CSS variables for consistency. Dominant colors with sharp accents outperform timid, evenly-distributed palettes.
- **Motion**: Use animations for effects and micro-interactions. Prioritize Tailwind's built-in animation utilities and CSS-only solutions. For complex animations, use Framer Motion if available. Focus on high-impact moments: one well-orchestrated page load with staggered reveals creates more delight than scattered micro-interactions. Use scroll-triggering and hover states that surprise.
- **Spatial Composition**: Unexpected layouts. Asymmetry. Overlap. Diagonal flow. Grid-breaking elements. Generous negative space OR controlled density. Leverage Tailwind's grid and flexbox utilities creatively.
- **Backgrounds & Visual Details**: Create atmosphere and depth rather than defaulting to solid colors. Add contextual effects and textures that match the overall aesthetic. Apply creative forms like gradient meshes, noise textures, geometric patterns, layered transparencies, dramatic shadows, decorative borders, and grain overlays using Tailwind classes and custom CSS where needed.

NEVER use generic AI-generated aesthetics like overused font families (Inter, Roboto, Arial, system fonts), cliched color schemes (particularly purple gradients on white backgrounds), predictable layouts and component patterns, and cookie-cutter design that lacks context-specific character.

Interpret creatively and make unexpected choices that feel genuinely designed for the context. No design should be the same. Vary between light and dark themes, different fonts, different aesthetics. NEVER converge on common choices (Space Grotesk, for example) across generations.

**IMPORTANT**: Match implementation complexity to the aesthetic vision. Maximalist designs need elaborate code with extensive animations and effects. Minimalist or refined designs need restraint, precision, and careful attention to spacing, typography, and subtle details. Elegance comes from executing the vision well.

## Tailwind-Specific Guidance

- Extend the Tailwind theme in `tailwind.config.ts` for project-wide design tokens (colors, fonts, spacing scales).
- Use `@apply` sparingly -- prefer utility classes in JSX.
- Use Tailwind's arbitrary value syntax (`bg-[#1a1a2e]`, `text-[clamp(1rem,2vw,1.5rem)]`) for one-off values that don't warrant theme extension.
- Leverage Tailwind's `group-hover`, `peer`, and container query utilities for interactive states.
- Use `dark:` variants for dark mode support when appropriate.