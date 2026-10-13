interface O { a: number; b: number; }
function main(c: boolean): number {
  const o1: O = { a: 1, b: 2 };
  const o2: O = { a: 3, b: 4 };
  return (c ? o1 : o2)?.["a"] ?? -1;
}
console.log(main(true))
