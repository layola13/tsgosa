interface P {
  x: number;
}
function f(k: keyof P): number {
  return k.length;
}
function main(): number {
  return f("x");
}
console.log(main());
