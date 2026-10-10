enum E { A = 2, B = 5 }
function cmp(e: E): i32 { return e == E.B ? 1 : 0; }
function main(): i32 {
  console.log(cmp(E.B));
  console.log(cmp(E.A));
  return 0;
}
