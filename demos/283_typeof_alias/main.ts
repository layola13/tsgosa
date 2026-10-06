const v = 42;
type T = typeof v;
function main(): number {
  const x: T = 1;
  return x;
}
console.log(main());
