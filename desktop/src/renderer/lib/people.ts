// How a person of a shared project looks (lib/here.ts, api.Person): their
// colour, one of the session hues (styles.css' [data-person-color]), the same
// for the same name on every AgentBox, and their initials. Shared with the
// phone app, so it lives in shared/people.ts.
export { initials, personColor, PERSON_COLORS, type PersonColor } from '../../shared/people.ts';
