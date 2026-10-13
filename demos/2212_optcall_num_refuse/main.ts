function getN(): number { return 3; }
function main(): number {
  return (getN() as unknown as { a: number })?.["a"] ?? -1;
}
console.log(main())
