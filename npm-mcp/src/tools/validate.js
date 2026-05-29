/**
 * thor_validate — run validation.
 */

import { convert } from "./convert.js";
import { runValidation } from "../lib/validator.js";

export async function validate(partnerFolder, options = {}) {
  const { controls, skipConversion, appType, concurrency, consensus } = options;

  // Convert if needed
  if (!skipConversion) {
    try {
      await convert(partnerFolder, appType || "SOFTWARE");
    } catch (err) {
      // If no Excel found but CSV exists, that's fine
      if (!err.message.includes("No .xlsx")) throw err;
    }
  }

  return runValidation(partnerFolder, { controls, concurrency, consensus });
}
