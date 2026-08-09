import { Placeholder } from "./Placeholder";

/**
 * The shopping console, and the app's index route.
 *
 * It is the index because it is where a buyer starts: everything else in this
 * app either follows from something bought here or explains it. #109 fills it
 * in — a text area for what the user wants, the merchant's catalogue, a
 * quantity per row and a tracker showing where each mandate stands.
 */
export function Console() {
  return (
    <Placeholder
      title="Shopping console"
      issue={109}
      answers="What the buyer asked for, what the merchant sells, and where every mandate stands."
    />
  );
}
