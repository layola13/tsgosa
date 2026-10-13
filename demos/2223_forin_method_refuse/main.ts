class C {
  m(): number { return 1; }
  count(): number {
    let t = 0;
    for (const k in this.m) { t += 1; }
    return t;
  }
}
function main(): number {
  const c = new C();
  return c.count();
}
console.log(main())
