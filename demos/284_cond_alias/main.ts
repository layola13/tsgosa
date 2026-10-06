type A<T> = T extends number ? 1 : 0;
function main(): number {
  const x: A<number> = 1;
  return x;
}
console.log(main());
