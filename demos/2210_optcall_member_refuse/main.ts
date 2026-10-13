interface O { a: number; }
class C {
  m(): O { return { a: 1 }; }
}
function main(): number {
  const c = new C();
  return c.m()?.["a"] ?? -1;
}
console.log(main())
