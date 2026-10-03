class Animal {
  legs: i32 = 4;
}
class Dog extends Animal {
  bark(): i32 {
    return 7;
  }
}
function main(): i32 {
  const d = new Dog();
  console.log(d.bark() + d.legs);
  return 0;
}
