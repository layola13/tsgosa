function getN(): number { return 3; }
function main(): number {
  let t = 0;
  for (const k in getN()) { t += 1; }
  return t;
}
console.log(main())
