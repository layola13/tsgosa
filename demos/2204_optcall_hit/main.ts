interface O { a: number; b: number; }
function getObj(): O { return { a: 10, b: 32 }; }
function main(): number {
  return getObj()?.["a"] ?? -1;
}
console.log(main())
