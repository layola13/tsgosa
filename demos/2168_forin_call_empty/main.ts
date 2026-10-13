interface E { }
function getE(): E { return {}; }
function main(): number {
  let t = 0;
  for (const k in getE()) { t += 1; }
  return t;
}
console.log(main())
