interface O { a: number; }
class C {
  m(): O { return { a: 1 }; }
}
function main(): number {
  const c = new C();
  let t = 0;
  for (const k in c.m()) { t += 1; }
  return t;
}
console.log(main())
