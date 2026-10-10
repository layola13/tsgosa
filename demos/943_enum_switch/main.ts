enum E { A = 1, B = 2 }
function main(): i32 {
  const e: E = E.B;
  switch (e) {
    case E.A: console.log(1); break;
    case E.B: console.log(2); break;
    default: console.log(0);
  }
  return 0;
}
