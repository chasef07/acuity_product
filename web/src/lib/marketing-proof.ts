export const ophthalmologyCaseStudyProof = {
  specialty: "Ophthalmology and optometry",
  scale: "Six locations and eight providers",
  implementation: [
    "Approximately one month of workflow mapping and configuration",
    "A one-month pilot at one Location",
    "Expansion to the remaining five Locations after the pilot",
  ],
  metrics: [
    { value: "500+", label: "reported monthly appointments booked into the EMR" },
    { value: "0", label: "dropped calls in the reported operating snapshot" },
    { value: "500", label: "estimated monthly staff hours returned" },
    { value: "$100K+", label: "estimated monthly revenue recovered" },
  ],
  operatingMetrics: [
    "Approximately 200 dropped calls per month before Acuity across six Locations",
    "70% of inbound calls reported resolved without transfer; 30% transferred to staff",
    "One OD schedule reported moving from roughly 50% to roughly 90% booked",
    "Approximately 300 Tasks reported created and completed per month",
  ],
  methodology: [
    "Appointments, Tasks, and call outcomes are reported from the practice systems and Acuity operating records.",
    "Staff time is estimated from the minutes of patient calls handled by Acuity.",
    "Recovered revenue is estimated from previously dropped-call volume and booking-value assumptions; it is not collected revenue.",
  ],
  limitations: [
    "Results describe one ophthalmology and optometry group and may not generalize to another practice.",
    "The figures are customer- and management-reported, not an independent audit.",
    "Exact comparison months, calculation worksheets, and source exports are not published on this page and remain limitations of the public evidence.",
  ],
}

export const ophthalmologyDeploymentProof = {
  eyebrow: "Six-location ophthalmology group · reported operating snapshot",
  title: "A deployed operation created measurable capacity.",
  description:
    "A reported operating snapshot from one ophthalmology deployment. Appointment and dropped-call counts are reported outcomes; staff time and recovered revenue are estimates, not collected revenue.",
  metrics: ophthalmologyCaseStudyProof.metrics,
}
