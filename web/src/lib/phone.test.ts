import assert from "node:assert/strict"
import test from "node:test"

import { formatUSPhone, normalizeUSPhone } from "./phone.ts"

test("normalizes common US phone number formats to canonical E.164", () => {
  for (const input of [
    "7277092035",
    "(727) 709-2035",
    "727-709-2035",
    "+1 727 709 2035",
    "1.727.709.2035",
    "+17277092035",
  ]) {
    assert.equal(normalizeUSPhone(input), "+17277092035")
  }
})

test("rejects incomplete, non-US, and malformed phone numbers", () => {
  for (const input of [
    "",
    "727709203",
    "+447277092035",
    "727-CALL-NOW",
    "++17277092035",
    "(727 709-2035",
  ]) {
    assert.equal(normalizeUSPhone(input), "")
  }
})

test("formats canonical US phone numbers for display", () => {
  assert.equal(formatUSPhone("+17277092035"), "(727) 709-2035")
  assert.equal(formatUSPhone("+442071838750"), "+442071838750")
})
