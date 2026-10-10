enum Color { R, G, B }
function pick(c: Color): i32 {
  switch (c) {
    case Color.R: return 1;
    case Color.G: return 2;
    default: return 3;
  }
}
function main(): i32 {
  console.log(pick(Color.R));
  console.log(pick(Color.B));
  return 0;
}
