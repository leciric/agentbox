import { createContext } from 'react';

// ChatRefContext is the chat a timeline is of, for a publish_page line to
// find its page among the project's, and a rich block to publish on Hatch
// from.
export const ChatRefContext = createContext('');
