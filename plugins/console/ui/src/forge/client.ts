import type {
  MergeInput,
  MergePreflight,
  MergeResult,
  AddCommentInput,
  AssignInput,
  Commit,
  FileDiff,
  ForgeComment,
  ListPrsQuery,
  Page,
  PendingPrSummary,
  PrRef,
  PullRequest,
  Repo,
  RepoRef,
  Review,
  ReviewDraft,
  ReviewValidation,
  ReviewInput,
} from './types'

/**
 * Forge-neutral client the Console codes to. The mock implements it today; G1
 * adds a typed HTTP client behind the same interface. No network or secrets
 * live in the GUI: any real implementation talks to the Torque/Cerberus backend.
 */
export interface ForgeClient {
  /** effect: read */
  getRepo(repo: RepoRef): Promise<Repo>
  /** effect: read */
  listPrs(query?: ListPrsQuery): Promise<Page<PullRequest>>
  /** effect: read */
  getPr(ref: PrRef): Promise<PullRequest>
  /** effect: read */
  getDiff(ref: PrRef): Promise<FileDiff[]>
  /** effect: read */
  listCommits(ref: PrRef): Promise<Commit[]>
  /** effect: read */
  listComments(ref: PrRef): Promise<ForgeComment[]>
  /** effect: write */
  addComment(ref: PrRef, input: AddCommentInput): Promise<ForgeComment>
  /** effect: write, ack */
  reviewPr(ref: PrRef, input: ReviewInput): Promise<Review>
  /** effect: read. Optional: clients without a preflight route fall back to local checks. */
  getMergePreflight?(ref: PrRef): Promise<MergePreflight>
  /** effect: read. Optional: absent when the forge has no validate route. */
  validateReview?(ref: PrRef, input: { headSha: string; comments: NonNullable<ReviewInput['comments']> }): Promise<ReviewValidation>
  /** effect: read. Optional draft persistence; null when none is saved. */
  getReviewDraft?(ref: PrRef): Promise<ReviewDraft | null>
  /** effect: write */
  saveReviewDraft?(ref: PrRef, draft: ReviewDraft): Promise<void>
  /** effect: write */
  deleteReviewDraft?(ref: PrRef): Promise<void>
  /** effect: write, ack */
  mergePr(ref: PrRef, input: MergeInput): Promise<MergeResult>
  /** effect: write */
  assign(ref: PrRef, input: AssignInput): Promise<PullRequest>
  /** effect: read */
  listPendingPrs(): Promise<PendingPrSummary[]>
}
