import assert from "node:assert/strict"
import test from "node:test"
import { readPatientStatusOverrides, isReviewPatientStatus } from "./call-patient-status.ts"

test("local patient corrections round-trip all supported values",()=>{
  const values = {first:"new",second:"existing",third:"unknown"}
  assert.deepEqual(readPatientStatusOverrides(JSON.stringify(values)),values)
  assert.deepEqual(readPatientStatusOverrides(null),{})
  assert.equal(isReviewPatientStatus("new"),true)
  assert.equal(isReviewPatientStatus("invented"),false)
})

test("corrupt local corrections fail without being replaced",()=>{
  for(const raw of ['null','[]','{"first":"invalid"}','{"first":null}','broken']) assert.throws(()=>readPatientStatusOverrides(raw))
})
