interface T { p: number; q: number; r: number; }
function getT(n: number): T { return { p: n, q: n, r: n }; }
function main(): number {
  return getT(5)?.["q"] ?? -1;
}
console.log(main())
