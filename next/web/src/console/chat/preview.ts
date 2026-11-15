/** A message excerpt as one plain line: markdown markers removed, links and code kept as their text. */
export function previewLine(text: string | undefined): string {
  if (!text) return ''
  return text
    .replace(/```[a-z]*\n?/gi, '')
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/(\*\*|__)(?=\S)(.+?)(?<=\S)\1/g, '$2')
    .replace(/(^|[\s(])([*_])(?=\S)([^*_\n]+?)(?<=\S)\2(?=$|[\s).,;:!?])/g, '$1$3')
    .replace(/~~(.+?)~~/g, '$1')
    .replace(/^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+|\d+\.\s+)/gm, '')
    .replace(/[*_`~]{2,}/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}
