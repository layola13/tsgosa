function f(): void { }
function main(): number {
  let t = 0;
  for (const k in f()) { t += 1; }
  return t;
}
console.log(main())
