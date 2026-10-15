// The review carousel (components/ReadyToReview.tsx) is the same in both
// layouts. UI v2 adds its decision under the header — Merge first, Request
// changes beside it, and where a request stands — by providing a component
// here (ReviewV2.tsx); with none provided, as in v1, the carousel is as it was.
import { createContext, type ComponentType } from 'react';
import type * as T from '../../../shared/api';

export type DecisionProps = {
  item: T.ReviewItem;
  // Its pull request as the project's list has it now.
  pr?: T.PullRequest;
  // canMerge is false for no pull request, or one merged or closed;
  // mergeReason is why an open one can't be merged yet.
  canMerge: boolean;
  mergeReason?: string;
  merging: boolean;
  onMerge: () => void;
  // onNext moves the walk on to the next agent waiting, or closes the
  // carousel after the last one: what sending a request for changes does.
  onNext: () => void;
};

export const ReviewDecisionContext = createContext<ComponentType<DecisionProps> | null>(null);
