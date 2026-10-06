type G = `hello ${string}`;
function id<T>(x: T, y: NoInfer<T>): i32 {
  return x;
}
function greet(g: G): string {
  return g;
}
function main(): i32 {
  const t: G = "hello x";
  const q: i32 = id(3, 4);
  console.log(t);
  console.log(greet(t).length + q);
  return greet(t).length + q;
}
console.log(main());
