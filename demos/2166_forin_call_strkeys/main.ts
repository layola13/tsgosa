interface S { x: string; y: string; }
function getS(): S { return { x: "p", y: "q" }; }
function main(): number {
  let t = 0;
  let s = "";
  for (const k in getS()) { t += 1; s += k; }
  console.log(s);
  return t;
}
console.log(main())
