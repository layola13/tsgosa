function f(x: number): number {
  switch (x) {
    case 1:
    case 2:
      return 10;
    default:
      return 20;
  }
}
function main(): number {
  return f(1) + f(2) + f(9);
}
console.log(main());
