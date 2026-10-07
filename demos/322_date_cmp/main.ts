function main(): number {
  const t = Date.now();
  const r1 = t > 0 ? 1 : 0;
  const r2 = t === t ? 1 : 0;
  let r3 = 0;
  if (t > 0) {
    r3 = 1;
  }
  console.log(r1);
  console.log(r2);
  console.log(r3);
  return r1 + r2 * 10 + r3 * 100;
}
console.log(main());
