function id<T>(x: T, y: NoInfer<T>): i32 {
  return x;
}
function main(): i32 {
  const s: string = String.fromCharCode(65, 66, 67);
  const q: i32 = id(3, 4);
  console.log(s);
  console.log(s.length + q);
  return s.length + q;
}
console.log(main());
