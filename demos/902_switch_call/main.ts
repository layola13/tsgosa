function kind(v: i32): i32 { return v; }
function main(): i32 {
  const x = 2;
  switch (kind(x)) {
    case 1: console.log(1); break;
    case 2: console.log(2); break;
    default: console.log(0);
  }
  return 0;
}
