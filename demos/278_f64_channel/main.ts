function f(): f64 { return 7.5; }
function main(): number {
  const y: f64 = 7.5;
  console.log(y);
  const c = y + 0.5;
  console.log(c);
  console.log(f());
  if (c == 8.0) {
    return 1;
  }
  return 0;
}
console.log(main());
