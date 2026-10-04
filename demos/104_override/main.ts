class Animal {
  voice(): i32 {
    return 1;
  }
}
class Dog extends Animal {
  voice(): i32 {
    return 2;
  }
}
function main(): i32 {
  const d = new Dog();
  const a = new Animal();
  console.log(a.voice(), d.voice());
  return 0;
}