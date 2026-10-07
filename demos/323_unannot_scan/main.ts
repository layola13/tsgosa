function main(): number {
  const a = [5, 3, 8, 1];
  const r1 = a.indexOf(8);
  const r2 = a.includes(3) ? 1 : 0;
  const r3 = a.lastIndexOf(1);
  console.log(r1);
  console.log(r2);
  console.log(r3);
  return r1 + r2 * 10 + r3 * 100;
}
console.log(main());
